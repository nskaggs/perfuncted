//go:build linux

package input

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// Losing directory ownership between store acquisition and a token operation
// must fail closed without falling back to cwd or any global token path.
func TestPortalTokenRevokedDirectoryDescriptorCannotStoreOrConsume(t *testing.T) {
	lock, err := lockPortalTokenStore(portalTokenTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Close(lock.dirfd); err != nil {
		t.Fatal(err)
	}
	lock.dirfd = -1
	if err := lock.store("private-token"); !errors.Is(err, unix.EBADF) {
		t.Fatalf("revoked directory descriptor accepted token write: %v", err)
	}
	if got, err := lock.consume(); got != "" || !errors.Is(err, unix.EBADF) {
		t.Fatalf("revoked directory descriptor exposed token=%q err=%v", got, err)
	}
}
