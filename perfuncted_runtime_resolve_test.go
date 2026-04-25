package perfuncted

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestResolveRuntimePreservesHostDesktopWhenNoSessionOverride(t *testing.T) {
	t.Setenv("DISPLAY", ":99")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	rt, err := resolveRuntime(Options{})
	if err != nil {
		t.Fatalf("resolveRuntime: %v", err)
	}
	if got := rt.Display(); got != ":99" {
		t.Fatalf("display = %q, want :99", got)
	}
}

func TestResolveRuntimeClearsHostDisplayForExplicitSession(t *testing.T) {
	t.Setenv("DISPLAY", ":99")
	t.Setenv("WAYLAND_DISPLAY", "wayland-host")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	t.Setenv("SWAYSOCK", "/run/user/1000/sway-ipc.123.sock")

	rt, err := resolveRuntime(Options{
		XDGRuntimeDir:      "/tmp/perfuncted-xdg-test",
		WaylandDisplay:     "wayland-1",
		DBusSessionAddress: "unix:path=/tmp/perfuncted-xdg-test/bus",
	})
	if err != nil {
		t.Fatalf("resolveRuntime: %v", err)
	}
	if got := rt.Get("XDG_RUNTIME_DIR"); got != "/tmp/perfuncted-xdg-test" {
		t.Fatalf("XDG_RUNTIME_DIR = %q", got)
	}
	if got := rt.Get("WAYLAND_DISPLAY"); got != "wayland-1" {
		t.Fatalf("WAYLAND_DISPLAY = %q", got)
	}
	if got := rt.Display(); got != "" {
		t.Fatalf("DISPLAY = %q, want empty", got)
	}
	if got := rt.Get("SWAYSOCK"); got != "" {
		t.Fatalf("SWAYSOCK = %q, want empty", got)
	}
}

func TestRuntimeSocketPathResolvesRelativeWaylandDisplay(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_RUNTIME_DIR=/tmp/perfuncted-host",
		"WAYLAND_DISPLAY=wayland-0",
	})

	if got := rt.SocketPath(); got != "/tmp/perfuncted-host/wayland-0" {
		t.Fatalf("SocketPath = %q, want /tmp/perfuncted-host/wayland-0", got)
	}
}

func TestRuntimeSocketPathKeepsAbsoluteWaylandDisplay(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_RUNTIME_DIR=/tmp/perfuncted-host",
		"WAYLAND_DISPLAY=/run/user/1000/wayland-1",
	})

	if got := rt.SocketPath(); got != "/run/user/1000/wayland-1" {
		t.Fatalf("SocketPath = %q, want /run/user/1000/wayland-1", got)
	}
}
