//go:build linux

package input

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Grant rotation must replace the private directory entry, not follow a
// preexisting symlink and overwrite a separate user's data.
func TestPortalTokenRotationReplacesSymlinkWithoutTouchingTarget(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	outer := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(outer, []byte("external credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if err := os.Symlink(outer, path); err != nil {
		t.Fatal(err)
	}
	if err := lock.store("newly-authorized-grant"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outer); err != nil || string(data) != "external credentials" {
		t.Fatalf("external target changed: %q, %v", data, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("grant is not private regular file: %v, %v", info, err)
	}
	token, err := lock.consume()
	if err != nil || token != "newly-authorized-grant" {
		t.Fatalf("rotated grant = %q, %v", token, err)
	}
}

// Restore-token state is secret-bearing; its live lock and directory
// descriptors must not be inherited by arbitrary processes started later.
func TestPortalTokenLockDescriptorsCloseOnExec(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, fd := range []int{lock.fd, lock.dirfd} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatal(err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("sensitive token-store fd %d inherited across exec", fd)
		}
	}
}
