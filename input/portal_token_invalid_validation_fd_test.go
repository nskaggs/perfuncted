//go:build linux

package input

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// The restore-token trust boundary must reject a descriptor that was closed
// after it was opened, rather than treating an unavailable stat as ownership
// proof or falling back to an untrusted filesystem path.
func TestPortalPrivateFileValidationRejectsClosedDescriptor(t *testing.T) {
	if err := validatePortalPrivateFile(-1); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid descriptor passed ownership validation: %v", err)
	}
}

// Cleanup is safe for a nil owner, including when startup rejected the token
// store before the lock object was established.
func TestPortalTokenNilOwnerCloseDoesNotTouchDescriptors(t *testing.T) {
	var lock *portalTokenLock
	if err := lock.Close(); err != nil {
		t.Fatalf("nil token lock close: %v", err)
	}
}
