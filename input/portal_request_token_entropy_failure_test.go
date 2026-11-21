package input

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingPortalEntropyReader struct{ err error }

func (r failingPortalEntropyReader) Read([]byte) (int, error) { return 0, r.err }

func TestPortalRequestTokenRejectsUnavailableEntropy(t *testing.T) {
	// A portal request must not proceed with a predictable token when the
	// cryptographic source fails. Restore the process-global reader in cleanup.
	original := rand.Reader
	failure := io.ErrUnexpectedEOF
	rand.Reader = failingPortalEntropyReader{err: failure}
	t.Cleanup(func() { rand.Reader = original })

	token, err := portalHandleToken()
	if token != "" || !errors.Is(err, failure) || !strings.Contains(err.Error(), "create request token") {
		t.Fatalf("portalHandleToken() = (%q, %v), want empty token and wrapped entropy error", token, err)
	}
}

// Neither portal session creation nor an ordinary portal request may begin
// D-Bus authorization with an unguessable handle unavailable. Both must fail
// at the entropy boundary even when no connection is present.
func TestPortalSessionAndRequestRejectMissingEntropyBeforeBusUse(t *testing.T) {
	original := rand.Reader
	failure := io.ErrUnexpectedEOF
	rand.Reader = failingPortalEntropyReader{err: failure}
	t.Cleanup(func() { rand.Reader = original })

	if session, err := portalCreateSession(context.Background(), nil, ""); session != "" || !errors.Is(err, failure) {
		t.Fatalf("session creation with failed entropy = (%q, %v), want no session and wrapped entropy error", session, err)
	}
	if results, err := portalRequest(context.Background(), nil, "", "CreateSession", nil); results != nil || !errors.Is(err, failure) {
		t.Fatalf("portal request with failed entropy = (%v, %v), want no results and wrapped entropy error", results, err)
	}
}
