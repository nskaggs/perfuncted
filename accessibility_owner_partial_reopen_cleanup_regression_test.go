package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A provider can allocate a replacement backend and then fail its final
// readiness checks. Reopen must close that partial replacement but preserve
// the previously serving generation and its capability to answer queries.
func TestAccessibilityReopenClosesPartiallyInitializedReplacementWithoutRetiringOriginal(t *testing.T) {
	denied := errors.New("synthetic replacement readiness verification failed")
	original := newOwnerLifecycleBackend(19, "applications", "reopen")
	var partials []*ownerLifecycleBackend
	original.reopen = func(context.Context) (accessibility.Backend, error) {
		partial := newOwnerLifecycleBackend(20, "applications")
		partials = append(partials, partial)
		return partial, denied
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, original)
	owner := accessibilityOwnerForTest(t, session)
	t.Cleanup(func() { _ = session.Close() })
	for attempt := 0; attempt < 2; attempt++ {
		if err := session.Accessibility.Reopen(context.Background()); !errors.Is(err, denied) {
			t.Fatalf("reopen %d hid provider readiness error: %v", attempt, err)
		}
		if !owner.hasBackend() || original.closeCalls.Load() != 0 {
			t.Fatalf("reopen %d retired usable original backend: available=%t closes=%d", attempt, owner.hasBackend(), original.closeCalls.Load())
		}
		if applications, err := session.Accessibility.Applications(context.Background()); err != nil || len(applications) != 0 {
			t.Fatalf("reopen %d stopped original application queries: apps=%v err=%v", attempt, applications, err)
		}
	}
	if len(partials) != 2 {
		t.Fatalf("replacement attempts = %d, want two", len(partials))
	}
	for i, partial := range partials {
		if partial.closeCalls.Load() != 1 {
			t.Fatalf("failed replacement %d closed %d times, want once", i, partial.closeCalls.Load())
		}
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing owning session: %v", err)
	}
	if original.closeCalls.Load() != 1 {
		t.Fatalf("original generation closed %d times, want once", original.closeCalls.Load())
	}
}
