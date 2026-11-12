//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A grant cannot be returned while its durable record remains replayable.
// Simulate an unlink denial after a valid credential has been read, then
// recover permissions and prove that the same grant is still available once.
func TestPortalRestoreTokenUnlinkFailureDoesNotRevealReplayableGrant(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unlink-permission denial requires an unprivileged user")
	}
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	const token = "single-use-private-grant"
	if err := lock.store(token); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	got, err := lock.consume()
	if got != "" || (!errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM)) {
		t.Fatalf("unlink-denied grant leaked: value=%q err=%v", got, err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lock.consume(); got != token || err != nil {
		t.Fatalf("retry after unlink recovery: value=%q err=%v", got, err)
	}
	if got, err := lock.consume(); got != "" || err != nil {
		t.Fatalf("consumed grant replayed: value=%q err=%v", got, err)
	}
}

// A failed permission check during rotation cannot replace or discard the
// previously stored grant, and recovery must not leave a pending new token.
func TestPortalRestoreTokenPermissionDeniedRotationPreservesExistingGrant(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("write-permission denial requires an unprivileged user")
	}
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	const previous = "previous-private-grant"
	if err := lock.store(previous); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	if err := lock.store("unauthorized-replacement"); !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) {
		t.Fatalf("rotation without directory write permission: %v", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lock.consume(); got != previous || err != nil {
		t.Fatalf("denied rotation altered original credential: value=%q err=%v", got, err)
	}
	if got, err := lock.consume(); got != "" || err != nil {
		t.Fatalf("denied rotation left a credential behind: value=%q err=%v", got, err)
	}
}
