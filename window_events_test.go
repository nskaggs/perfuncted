package perfuncted_test

import (
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type eventManager struct {
	*pftest.Manager
	events <-chan window.Event
}

func (m *eventManager) WindowEvents() <-chan window.Event {
	return m.events
}

func (*eventManager) SupportedOperations() []string {
	return []string{"events"}
}

func TestWindowBundleEventsExposesBackendEventSource(t *testing.T) {
	events := make(chan window.Event, 1)
	session := perfuncted.NewSessionForTesting(
		nil,
		nil,
		&eventManager{Manager: &pftest.Manager{}, events: events},
		nil,
		nil,
	)
	defer session.Close()

	gotEvents, err := session.Windows.Events()
	if err != nil {
		t.Fatalf("WindowBundle.Events: %v", err)
	}
	want := perfuncted.WindowEvent{
		Kind: perfuncted.WindowAddedEvent,
		ID:   "17",
	}
	events <- want
	if got := <-gotEvents; got != want {
		t.Fatalf("WindowBundle.Events event = %#v, want %#v", got, want)
	}
}

func TestWindowBundleEventsRejectsBackendWithoutEventSource(t *testing.T) {
	session := perfuncted.NewSessionForTesting(nil, nil, &pftest.Manager{}, nil, nil)
	defer session.Close()

	if _, err := session.Windows.Events(); !errors.Is(err, perfuncted.ErrUnsupported) {
		t.Fatalf("WindowBundle.Events error = %v, want ErrUnsupported", err)
	}
}
