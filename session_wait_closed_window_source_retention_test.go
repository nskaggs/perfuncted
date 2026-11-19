package perfuncted

import (
	"context"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

// The backend's lifecycle-event producer may close independently of the
// session. An exhausted producer must not cause spurious repeated wakeups or
// invalidate independent application notifications.
func TestWaitHubClosedWindowChangeStreamPreservesOtherNotifications(t *testing.T) {
	backend := newHandleWindowManager(window.Info{NativeID: "w-1"})
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	first := session.waitChanges()
	backend.changes <- struct{}{}
	select {
	case <-first.done:
		if first.change.source != "window" || first.change.event != nil {
			t.Fatalf("window source was misattributed: %+v", first.change)
		}
	case <-time.After(time.Second):
		t.Fatal("window source did not notify the invalidation hub")
	}

	close(backend.changes)
	next := session.waitChanges()
	select {
	case <-next.done:
		t.Fatalf("closed window producer invented a wakeup: %+v", next.change)
	case <-time.After(10 * time.Millisecond):
	}
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("closed window source corrupted an independent wakeup: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("application notification lost after closing window source")
	}
}

// Direct accessibility event sources may terminate independently of the
// session; once their stream closes the invalidation hub must remain usable.
type directlyClosingAccessibilitySource struct {
	waitAccessibilityBackend
	events chan accessibility.Event
	opened chan struct{}
}

func (s *directlyClosingAccessibilitySource) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	close(s.opened)
	return s.events, nil
}

func TestWaitHubClosedAccessibilityStreamPreservesOtherNotifications(t *testing.T) {
	source := &directlyClosingAccessibilitySource{
		events: make(chan accessibility.Event, 1),
		opened: make(chan struct{}),
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.Accessibility.backend = source
	first := session.waitChanges()
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("accessibility event source was not opened")
	}
	source.events <- accessibility.Event{Kind: "focus"}
	select {
	case <-first.done:
		if first.change.source != "accessibility" || first.change.event == nil || first.change.event.Kind != "focus" {
			t.Fatalf("accessibility event attributed incorrectly: %+v", first.change)
		}
	case <-time.After(time.Second):
		t.Fatal("accessibility stream did not notify the invalidation hub")
	}
	close(source.events)
	next := session.waitChanges()
	select {
	case <-next.done:
		t.Fatalf("closed accessibility stream invented wakeup: %+v", next.change)
	case <-time.After(10 * time.Millisecond):
	}
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("closed accessibility stream corrupted application event: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("application notification lost after closing accessibility stream")
	}
}
