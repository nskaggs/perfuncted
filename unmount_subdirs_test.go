package perfuncted

import (
	"context"
	"os"
	"testing"
)

func TestUnmountSubdirs_NonExistentDir(t *testing.T) {
	// Should not panic or error on non-existent directory
	unmountSubdirsContext(context.Background(), "/nonexistent/path/that/does/not/exist")
}

func TestUnmountSubdirs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should not panic or error on a temp dir (no FUSE mounts under it)
	unmountSubdirsContext(context.Background(), dir)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir was removed after unmountSubdirs: %v", err)
	}
}

func TestUnmountSubdirs_EmptyString(t *testing.T) {
	// Should not panic or error on empty string
	unmountSubdirsContext(context.Background(), "")
}

func TestUnmountSubdirs_FileInsteadOfDir(t *testing.T) {
	f, err := os.CreateTemp("", "unmount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	// Should not panic when called with a file path
	unmountSubdirsContext(context.Background(), path)
}
