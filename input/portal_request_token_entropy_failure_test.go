package input

import (
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
