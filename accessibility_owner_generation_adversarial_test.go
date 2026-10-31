package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A provider that refuses to advance its generation cannot safely publish
// reused node handles after reconnect; the owner must abandon the old backend
// rather than silently exposing potentially stale accessibility identities.
type stubbornAccessibilityGeneration struct{ *ownerLifecycleBackend }

func (b *stubbornAccessibilityGeneration) Invalidate(accessibility.NodeID) {}

func TestAccessibilityOwnerRejectedStaleGenerationRevokesOwnerAndClosesBothBackends(t *testing.T) {
	old := newOwnerLifecycleBackend(37, "applications", "reopen")
	freshBase := newOwnerLifecycleBackend(37, "applications", "reopen")
	fresh := &stubbornAccessibilityGeneration{freshBase}
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)
	err := session.Accessibility.Reopen(context.Background())
	if !errors.Is(err, accessibility.ErrStaleGeneration) {
		t.Fatalf("stale provider could reopen with reused node IDs: %v", err)
	}
	if owner.hasBackend() {
		t.Fatal("stale generation was published after failed validation")
	}
	if _, err := session.Accessibility.Applications(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("abandoned owner allowed stale application query: %v", err)
	}
	if old.closeCalls.Load() != 1 || freshBase.closeCalls.Load() != 1 {
		t.Fatalf("old/fresh cleanup counts = %d/%d, want 1/1", old.closeCalls.Load(), freshBase.closeCalls.Load())
	}
	capability := session.Capability(CapabilityAccessibility)
	if capability.Available || !errors.Is(capability.Failure, ErrUnavailable) || !strings.Contains(capability.Failure.Error(), "generation") {
		t.Fatalf("stale provider still advertised as available: %+v", capability)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing abandoned session: %v", err)
	}
	if old.closeCalls.Load() != 1 || freshBase.closeCalls.Load() != 1 {
		t.Fatal("session close double-closed abandoned generations")
	}
}

func TestAccessibilityOwnerRejectsReopenWithNilOrPartiallyFailedProvider(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reopen  func(context.Context) (accessibility.Backend, error)
		failure error
	}{
		{"nil backend", func(context.Context) (accessibility.Backend, error) { return nil, nil }, accessibility.ErrDisconnected},
		{"provider error", func(context.Context) (accessibility.Backend, error) {
			return nil, errors.New("synthetic reconnect refused")
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := newOwnerLifecycleBackend(9, "applications", "reopen")
			old.reopen = tc.reopen
			session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
			t.Cleanup(func() { _ = session.Close() })
			err := session.Accessibility.Reopen(context.Background())
			if err == nil {
				t.Fatal("invalid backend was accepted")
			}
			if tc.failure != nil && !errors.Is(err, tc.failure) {
				t.Fatalf("reconnect error %v, want %v", err, tc.failure)
			}
			if !accessibilityOwnerForTest(t, session).hasBackend() {
				t.Fatal("failed fresh backend invalidated still-usable prior generation")
			}
			if old.closeCalls.Load() != 0 {
				t.Fatal("old backend closed despite a failed replacement")
			}
			if _, err := session.Accessibility.Applications(context.Background()); err != nil {
				t.Fatalf("old generation no longer usable: %v", err)
			}
		})
	}
}
