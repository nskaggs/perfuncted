package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Capability advertisements cannot authorize a stream from a backend that
// does not implement EventSource. A rejected event request must not alter the
// underlying provider or accidentally publish a channel with no event owner.
func TestAccessibilityBundleEventsRejectsUnsupportedAndUnavailableSources(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if stream, err := nilBundle.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle admitted events: stream=%v err=%v", stream, err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing backend admitted events: stream=%v err=%v", stream, err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session admitted events: stream=%v err=%v", stream, err)
	}

	provider := newOwnerLifecycleBackend(31, "events", "applications")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	t.Cleanup(func() { _ = session.Close() })
	original := session.Accessibility.backend
	session.Accessibility.backend = &coreOnlyAccessibilityBackend{Backend: provider}
	defer func() { session.Accessibility.backend = original }()
	stream, err := session.Accessibility.Events(context.Background(), accessibility.EventOptions{Buffer: 2})
	if stream != nil || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("provider without EventSource exposed stream=%v err=%v", stream, err)
	}
	if provider.eventCalls.Load() != 0 || provider.closeCalls.Load() != 0 {
		t.Fatalf("unsupported event source dispatched or closed provider: calls=%d closed=%d", provider.eventCalls.Load(), provider.closeCalls.Load())
	}
	session.Accessibility.backend = original
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("rejected event subscription damaged live provider: %v", err)
	}
}
