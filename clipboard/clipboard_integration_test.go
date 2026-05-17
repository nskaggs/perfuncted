//go:build integration
// +build integration

package clipboard

import (
	"net"
	"path/filepath"
	"testing"
)

func TestOpenCapturesSessionEnv(t *testing.T) {
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, "wayland-77")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	defer ln.Close()

	t.Setenv("WAYLAND_DISPLAY", "wayland-77")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(runtimeDir, "bus"))

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
	if got := env["XDG_RUNTIME_DIR"]; got != runtimeDir {
		t.Fatalf("XDG_RUNTIME_DIR = %q", got)
	}
	if got := env["DBUS_SESSION_BUS_ADDRESS"]; got != "unix:path="+filepath.Join(runtimeDir, "bus") {
		t.Fatalf("DBUS_SESSION_BUS_ADDRESS = %q", got)
	}
}

func TestOpenPrefersCapturedWaylandEnv(t *testing.T) {
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, "wayland-88")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	defer ln.Close()

	t.Setenv("WAYLAND_DISPLAY", "wayland-88")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
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
