//go:build linux

package window

import (
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

// Window-added/changed events can omit their outer ID. Preserve the ID from
// the embedded window so downstream consumers can still reconcile by identity.
func TestGnomeNativeEventsRecoverMissingOuterWindowIdentity(t *testing.T) {
	src := make(chan gnomebridge.WindowEvent, 3)
	released := make(chan struct{})
	m := &GnomeNativeManager{events: make(chan Event, 3), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "211", Title: "first"}}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "212", Title: "changed"}}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowRemovedEvent, ID: "213"}
	close(src)
	go m.forwardWindowEvents(src, func() { close(released) })
	select {
	case <-m.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("event forwarder stalled")
	}
	select {
	case <-released:
	default:
		t.Fatal("subscription release not invoked")
	}
	var events []Event
	for event := range m.events {
		events = append(events, event)
	}
	if len(events) != 3 || events[0].ID != "211" || events[0].Window.NativeID != "211" || events[1].ID != "212" || events[1].Window.NativeID != "212" || events[2].ID != "213" || events[2].Kind != WindowRemovedEvent {
		t.Fatalf("missing outer IDs incorrectly reconciled: %+v", events)
	}
}
