package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A reconnect request must not manufacture a backend from an absent, closed,
// or non-reconnectable accessibility provider. In particular, advertising the
// capability does not authorize casting an arbitrary backend to its owner.
func TestAccessibilityBundleReopenRejectsAbsentClosedOrUnownedBackend(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if err := nilBundle.Reopen(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle reopened: %v", err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("absent provider reopened: %v", err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session reopened: %v", err)
	}

	owned := newOwnerLifecycleBackend(8, "applications", "reopen")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, owned)
	t.Cleanup(func() { _ = session.Close() })
	owner := session.Accessibility.backend
	direct := &coreOnlyAccessibilityBackend{Backend: owned}
	session.Accessibility.backend = direct
	defer func() { session.Accessibility.backend = owner }()
	if err := session.Accessibility.Reopen(context.Background()); !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("unowned backend reconnect: %v", err)
	}
	if owned.closeCalls.Load() != 0 {
		t.Fatal("unsupported reconnect unexpectedly closed live owner backend")
	}
	if !session.Capability(CapabilityAccessibility).Available {
		t.Fatal("unsupported reconnect disabled still-owned capability")
	}
	session.Accessibility.backend = owner
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("unsupported request corrupted original backend: %v", err)
	}
}
