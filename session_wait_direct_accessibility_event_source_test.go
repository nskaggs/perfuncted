package perfuncted

import (
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
	"time"
)

// Explicit, non-owned accessibility event sources must participate in the
// session's invalidation hub. The event attribution must remain attached to
// its original epoch rather than being replaced by later notifications.
func TestWaitChangesAcceptsDirectAccessibilityEventSource(t *testing.T) {
	source := newWaitAccessibilityEventBackend()
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.Accessibility.backend = source
	epoch := session.waitChanges()
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("direct event source subscription was not opened")
	}
	event := accessibility.Event{Kind: "state-changed", Property: "enabled"}
	source.events <- event
	select {
	case <-epoch.done:
		if epoch.change.source != "accessibility" || epoch.change.event == nil || epoch.change.event.Kind != event.Kind || epoch.change.event.Property != event.Property {
			t.Fatalf("direct event was not attributed to its epoch: %+v", epoch.change)
		}
	case <-time.After(time.Second):
		t.Fatal("direct event did not notify waiting session")
	}
	// Closing the producer must not close the session's hub or manufacture a
	// second event; notifications from independent sources remain functional.
	close(source.events)
	next := session.waitChanges()
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("unrelated invalidation inherited old event: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("closed direct event stream prevented application wake")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
}
