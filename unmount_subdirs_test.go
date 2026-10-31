package perfuncted

import (
	"context"
	"os"
	"testing"
)

func TestUnmountSubdirs_NonExistentDir(t *testing.T) {
	// Should not panic or error on non-existent directory
	if err := unmountSubdirsContext(context.Background(), "/nonexistent/path/that/does/not/exist"); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
}

func TestUnmountSubdirs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should not panic or error on a temp dir (no FUSE mounts under it)
	if err := unmountSubdirsContext(context.Background(), dir); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir was removed after unmountSubdirs: %v", err)
	}
}

func TestUnmountSubdirs_EmptyString(t *testing.T) {
	// Should not panic or error on empty string
	if err := unmountSubdirsContext(context.Background(), ""); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
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
	if err := unmountSubdirsContext(context.Background(), path); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
}
