//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A restore-token store is bound to the directory descriptor it validated.
// If that descriptor becomes unusable, neither a read nor a write may fall
// back to a path-based operation or report successful credential persistence.
func TestPortalTokenStoreInvalidDirectoryDescriptorFailsClosed(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	stateDir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := lock.store("existing-private-token"); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(stateDir, portalRestoreTokenFile)
	// Invalidate the owned directory descriptor, without changing the path.
	if err := unix.Close(lock.dirfd); err != nil {
		t.Fatal(err)
	}
	lock.dirfd = -1
	if token, err := lock.consume(); token != "" || !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid directory read exposed token=%q err=%v", token, err)
	}
	if err := lock.store("not-persisted"); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid directory wrote token: %v", err)
	}
	if data, err := os.ReadFile(tokenPath); err != nil || string(data) != "existing-private-token\n" {
		t.Fatalf("invalid descriptor changed durable grant: %q %v", data, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("closing remaining token lock: %v", err)
	}
	reopened, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("invalid prior owner kept store locked: %v", err)
	}
	defer reopened.Close()
	if token, err := reopened.consume(); err != nil || token != "existing-private-token" {
		t.Fatalf("failed operation consumed/changed saved grant: %q %v", token, err)
	}
}

func TestPortalTokenStoreInvalidLockDescriptorRefusesFurtherOperations(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	if err := unix.Close(lock.fd); err != nil {
		t.Fatal(err)
	}
	lock.fd = -1
	if token, err := lock.consume(); token != "" || err == nil {
		t.Fatalf("released lock admitted token read: %q %v", token, err)
	}
	if err := lock.store("unauthorized"); err == nil {
		t.Fatal("released lock admitted token write")
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("closing remaining directory descriptor: %v", err)
	}
	reopened, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("released lock kept resource unavailable: %v", err)
	}
	defer reopened.Close()
	if token, err := reopened.consume(); err != nil || token != "" {
		t.Fatalf("unauthorized store survived invalid owner: %q %v", token, err)
	}
}
