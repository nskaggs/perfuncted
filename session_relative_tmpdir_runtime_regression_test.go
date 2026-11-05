package perfuncted

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unix-domain sockets and managed-session ownership require absolute paths.
// A relative TMPDIR must not publish a relative runtime or misdirect cleanup.
func TestManagedSessionRelativeTMPDIRUsesAbsolutePrivateRuntime(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("private-temp", 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "private-temp")
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatalf("relative TMPDIR startup fallback: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if !filepath.IsAbs(runtimeDir) {
		t.Fatalf("managed runtime path %q is relative", runtimeDir)
	}
	if !isManagedSessionDir(runtimeDir) {
		t.Fatalf("absolute private runtime %q was not recognized for cleanup", runtimeDir)
	}
	if !strings.HasPrefix(runtimeDir, "/tmp/") {
		t.Fatalf("relative TMPDIR did not use absolute safe fallback: %q", runtimeDir)
	}
	for _, socket := range []string{"bus", "wayland-1"} {
		if err := validateManagedSessionSocketPath(filepath.Join(runtimeDir, socket)); err != nil {
			t.Fatal(err)
		}
	}
}
