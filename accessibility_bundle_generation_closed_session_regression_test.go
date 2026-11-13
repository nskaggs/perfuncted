package perfuncted

import (
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
)

// A closed session must not expose a live generation identity or let callers
// invalidate a provider that no longer belongs to their session.
func TestAccessibilityGenerationAndInvalidationRejectClosedSession(t *testing.T) {
	backend := &bundleAccessibilityFake{gen: 41}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	original := session.Accessibility.backend
	session.Accessibility.backend = backend
	defer func() { session.Accessibility.backend = original }()
	if got := session.Accessibility.Generation(); got != 41 {
		t.Fatalf("initial generation=%d", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	id := accessibility.NodeID{BusName: "org.example", ObjectPath: "/node", Generation: 41}
	if got := session.Accessibility.Generation(); got != 0 {
		t.Errorf("closed session advertised generation %d", got)
	}
	session.Accessibility.Invalidate(id)
	if backend.gen != 41 {
		t.Errorf("closed session invalidated retired provider to generation %d", backend.gen)
	}
}
