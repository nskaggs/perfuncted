//go:build linux

package window

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

// A native event publisher must not stall while the window-events consumer
// is absent; its event channel is explicitly bounded and lossy.
func TestGnomeNativeEventForwarderNeverBlocksOnFullSubscriberQueue(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent, 150)
	release := make(chan struct{})
	manager := &GnomeNativeManager{events: make(chan Event, 1), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "18", Title: "private document"}}
	for i := 0; i < 100; i++ {
		source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "18"}}
	}
	source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowEventKind("not-a-supported-event")}
	close(source)
	go manager.forwardWindowEvents(source, func() { close(release) })
	select {
	case <-manager.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber blocked GNOME bridge dispatch")
	}
	select {
	case <-release:
	default:
		t.Fatal("forwarder did not cancel its GNOME event subscription")
	}
	events := []Event{}
	for event := range manager.WindowEvents() {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Kind != WindowAddedEvent || events[0].ID != "18" || events[0].Window.Title != "private document" {
		t.Fatalf("bounded queue lost first event or published unsupported kind: %+v", events)
	}
}

func TestGnomeNativeEventForwarderStopsWhenUnsubscribedWithIdleSource(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent)
	release := make(chan struct{})
	manager := &GnomeNativeManager{events: make(chan Event, 1), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	go manager.forwardWindowEvents(source, func() { close(release) })
	close(manager.stopEvents)
	select {
	case <-manager.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("closing owner did not stop event forwarding")
	}
	select {
	case <-release:
	default:
		t.Fatal("event subscription not released when owner stopped")
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("owner closed without closing its event stream")
	}
}

func TestGnomeNativeNilManagerIteratorFailsClosedWithoutEmittingWindow(t *testing.T) {
	var manager *GnomeNativeManager
	if manager.WindowEvents() != nil {
		t.Fatal("nil manager exposed live event source")
	}
	reports := 0
	for info, err := range manager.IterateWindows(context.Background()) {
		reports++
		if info.ID != 0 || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("nil manager produced window=%+v err=%v", info, err)
		}
	}
	if reports != 1 {
		t.Fatalf("expected single terminal error, got %d", reports)
	}
}
