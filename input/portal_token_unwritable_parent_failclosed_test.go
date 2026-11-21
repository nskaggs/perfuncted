//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A state path must not be fabricated through an unwritable parent. In
// particular, failed directory creation must never be treated as successful
// portal restore-token storage, and must not create a partial credential path.
func TestPortalTokenStateDirectoryFailsClosedOnUnwritableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the private parent write permission")
	}
	root := privatePortalTokenFixtureDir(t)
	parent := filepath.Join(root, "readonly")
	if err := os.Mkdir(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })

	state := filepath.Join(parent, "new-state", "perfuncted")
	fd, err := openPrivatePortalDirectory(state)
	if fd >= 0 {
		_ = unix.Close(fd)
		t.Fatal("unwritable state parent yielded open credential directory")
	}
	if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) {
		t.Fatalf("unwritable parent error = %v, want access denied", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "new-state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unwritable state parent created partial credential directory: %v", err)
	}
}
