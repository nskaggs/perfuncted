package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNestedEnvSkipsStaleDirsWithoutWaylandSocket(t *testing.T) {
	validDir := mustCreateNestedSessionDir(t, true)
	staleDir := mustCreateNestedSessionDir(t, false)

	oldGlob := nestedSessionGlob
	nestedSessionGlob = func(pattern string) ([]string, error) {
		return []string{staleDir, validDir}, nil
	}
	t.Cleanup(func() {
		nestedSessionGlob = oldGlob
	})

	xdg, wl, dbus, err := NestedEnv()
	if err != nil {
		t.Fatalf("NestedEnv: %v", err)
	}

	if xdg != validDir {
		t.Fatalf("XDG_RUNTIME_DIR = %q, want %q", xdg, validDir)
	}
	if wl != "wayland-1" {
		t.Fatalf("WAYLAND_DISPLAY = %q, want wayland-1", wl)
	}
	if dbus != "unix:path="+filepath.Join(validDir, "bus") {
		t.Fatalf("DBusSessionAddress = %q, want unix:path=%s/bus", dbus, validDir)
	}
}

func mustCreateNestedSessionDir(t *testing.T, withSocket bool) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	if !withSocket {
		return dir
	}

	socketPath := filepath.Join(dir, "wayland-1")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return dir
}
