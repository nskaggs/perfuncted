package env

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeSnapshotsDoNotLeakHostOrMutateOriginals(t *testing.T) {
	original := FromEnviron([]string{
		"DISPLAY=:19", "WAYLAND_DISPLAY=wayland-host", "XDG_RUNTIME_DIR=/run/user/19",
		"AT_SPI_BUS=host-accessibility", "ATSPI_BUS_ADDRESS=host-a11y",
		"AT_SPI_BUS_ADDRESS=host-legacy-a11y", "HYPRLAND_INSTANCE_SIGNATURE=host-signature",
		"SWAYSOCK=host-sway", "KEY=first=second", "INVALID", "=empty-key", "KEY=overridden=third",
	})
	if value, ok := original.Lookup("KEY"); !ok || value != "overridden=third" {
		t.Fatalf("duplicate key or embedded '=' parsed incorrectly: %q %t", value, ok)
	}
	if original.Has("INVALID") {
		t.Fatal("non-environment value unexpectedly included")
	}
	if value, ok := original.Lookup(""); !ok || value != "empty-key" {
		t.Fatalf("empty key was not parsed verbatim %q %v", value, ok)
	}
	moved := original.WithSession("/run/user/42", "wayland-42", "unix:path=/run/user/42/bus")
	if moved.Has("DISPLAY") || moved.Has("SWAYSOCK") || moved.Has("HYPRLAND_INSTANCE_SIGNATURE") || moved.Has("ATSPI_BUS_ADDRESS") {
		t.Fatalf("managed session leaked host desktop routing: %v", moved.EnvList())
	}
	if got := moved.Get("WAYLAND_DISPLAY"); got != "wayland-42" {
		t.Fatalf("managed session display = %q", got)
	}
	if got := original.Get("DISPLAY"); got != ":19" {
		t.Fatalf("source changed after managed-session clone: %q", got)
	}
	isolated := moved.With("KEY", "child").Without("DBUS_SESSION_BUS_ADDRESS", "KEY")
	if isolated.Has("DBUS_SESSION_BUS_ADDRESS") || isolated.Has("KEY") {
		t.Fatalf("Without did not remove keys: %v", isolated.EnvList())
	}
	if moved.Get("KEY") != "overridden=third" || moved.Get("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatalf("With/Without mutated source runtime: %v", moved.EnvList())
	}
	if got := original.WithAccessibilityBus("   ").Get("ATSPI_BUS_ADDRESS"); got != "" {
		t.Fatalf("empty bus did not clear host address: %q", got)
	}
	if got := original.WithAccessibilityBus(" unix:path=/secure ").Get("ATSPI_BUS_ADDRESS"); got != "unix:path=/secure" {
		t.Fatalf("bus address not normalized: %q", got)
	}
}

func TestRuntimeEnvListIsDeterministicAndNeverInheritsParent(t *testing.T) {
	var empty Runtime
	if got := empty.EnvList(); got == nil || len(got) != 0 {
		t.Fatalf("empty runtime inherited process env: %#v", got)
	}
	if got := empty.Get("HOME"); got != "" {
		t.Fatalf("zero runtime leaked HOME: %q", got)
	}
	if got := empty.Display(); got != "" {
		t.Fatalf("zero runtime leaked display: %q", got)
	}
	src := FromEnviron([]string{"Z=3", "A=one=two", "M=5"})
	want := []string{"A=one=two", "M=5", "Z=3"}
	if got := src.EnvList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("non-deterministic env ordering: %v want %v", got, want)
	}
	out := src.EnvList()
	out[0] = "EVIL=1"
	if got := src.EnvList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated source snapshot: %v", got)
	}
}

func TestRuntimeSocketPathResolvesAbsoluteAndRelativeWithoutFallback(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "wayland-synthetic")
	relative := FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=wayland-synthetic"})
	if got := relative.SocketPath(); got != expected {
		t.Fatalf("relative socket resolved to %q instead of %q", got, expected)
	}
	absolute := relative.With("WAYLAND_DISPLAY", expected)
	if got := absolute.SocketPath(); got != expected {
		t.Fatalf("absolute socket resolved to %q", got)
	}
	if got := relative.Without("WAYLAND_DISPLAY").SocketPath(); got != "" {
		t.Fatalf("missing socket accepted: %q", got)
	}
	if got := FromEnviron([]string{"WAYLAND_DISPLAY=wayland-synthetic"}).SocketPath(); got != "" {
		t.Fatalf("relative socket without runtime directory accepted: %q", got)
	}
}

func TestRuntimeCurrentAndManagedEnvironOnlyExposeExpectedSession(t *testing.T) {
	t.Setenv("PF_ENV_RUNTIME_MARKER", "visible")
	current := Current()
	if current.Get("PF_ENV_RUNTIME_MARKER") != "visible" {
		t.Fatal("Current failed to capture environment")
	}
	os.Setenv("PF_ENV_RUNTIME_MARKER", "mutated")
	t.Cleanup(func() { _ = os.Setenv("PF_ENV_RUNTIME_MARKER", "visible") })
	if current.Get("PF_ENV_RUNTIME_MARKER") != "visible" {
		t.Fatal("Current snapshot changed after host mutation")
	}
	envList := Environ("/run/user/77", "wayland-77", "unix:path=/run/user/77/bus")
	envEntries := strings.Join(envList, "\n")
	for _, want := range []string{"XDG_RUNTIME_DIR=/run/user/77", "WAYLAND_DISPLAY=wayland-77", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/77/bus", "XDG_SESSION_TYPE=wayland"} {
		if !strings.Contains(envEntries, want) {
			t.Fatalf("managed environment missing %q: %s", want, envEntries)
		}
	}
}
