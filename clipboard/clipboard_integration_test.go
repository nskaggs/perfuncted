//go:build integration
// +build integration

package clipboard

import "testing"

func TestOpenCapturesSessionEnv(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-77")
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/perfuncted-test")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/perfuncted-test/bus")

	cb, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wl, ok := cb.(*extCmdClipboard)
	if !ok {
		t.Fatalf("clipboard type = %T, want *extCmdClipboard", cb)
	}

	env := make(map[string]string)
	for _, kv := range wl.env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}

	if got := env["WAYLAND_DISPLAY"]; got != "wayland-77" {
		t.Fatalf("WAYLAND_DISPLAY = %q", got)
	}
	if got := env["XDG_RUNTIME_DIR"]; got != "/tmp/perfuncted-test" {
		t.Fatalf("XDG_RUNTIME_DIR = %q", got)
	}
	if got := env["DBUS_SESSION_BUS_ADDRESS"]; got != "unix:path=/tmp/perfuncted-test/bus" {
		t.Fatalf("DBUS_SESSION_BUS_ADDRESS = %q", got)
	}
}

func TestOpenPrefersCapturedWaylandEnv(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-88")
	cb, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	wl := cb.(*extCmdClipboard)
	found := false
	for _, kv := range wl.env {
		if kv == "WAYLAND_DISPLAY=wayland-88" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("captured env missing WAYLAND_DISPLAY")
	}
}
