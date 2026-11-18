package perfuncted

import (
	"errors"
	"testing"
)

// Uninitialized bundle handles must fail closed before consulting capability
// metadata or issuing backend operations.
func TestUninitializedBundleAvailabilityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		base *bundleBase
	}{
		{name: "nil bundle", base: nil},
		{name: "bundle without session", base: &bundleBase{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.base.checkAvailable("clipboard.get", true); !errors.Is(err, ErrNilSession) {
				t.Fatalf("uninitialized bundle accepted advertised operation: %v", err)
			}
			if err := tc.base.checkBackend("clipboard.get", struct{}{}); !errors.Is(err, ErrNilSession) {
				t.Fatalf("uninitialized bundle accepted synthetic backend: %v", err)
			}
			// A diagnostic trace must be safe to attempt even after the
			// owner session has been discarded.
			tc.base.traceAction("clipboard", "get")
		})
	}
}
