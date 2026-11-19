package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A previously enumerated managed runtime may become inaccessible before its
// inode is inspected. Fail closed: never consider a permission error evidence
// that the directory is stale or that its contents can safely be removed.
func TestReapManagedRuntimePreservesDirectoryWhenInspectionIsDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses parent directory search permissions")
	}
	base := t.TempDir()
	preferred := filepath.Join(base, "inaccessible-parent")
	runtime := filepath.Join(preferred, "perfuncted-xdg-private-data")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(runtime, "keep")
	if err := os.WriteFile(payload, []byte("foreign runtime content"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", preferred)
	if err := os.Chmod(preferred, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(preferred, 0700); err != nil {
			t.Errorf("restore directory permissions: %v", err)
		}
	})
	err := reapSessionDir(context.Background(), runtime)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("inaccessible runtime not rejected: %v", err)
	}
	if err := os.Chmod(preferred, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(payload)
	if err != nil || string(data) != "foreign runtime content" {
		t.Fatalf("reaper damaged inaccessible runtime: data=%q err=%v", data, err)
	}
}
