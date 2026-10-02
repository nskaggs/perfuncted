//go:build linux
// +build linux

package window

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

func TestToWindowInfoPreservesNativeFields(t *testing.T) {
	got := toWindowInfo(gnomebridge.WindowInfo{
		ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", Class: "Terminal",
		PID: 42, X: 1, Y: 2, Width: 800, Height: 600,
		Active: true, Minimized: false, Maximized: true, Fullscreen: true,
	})
	if got.ID != 17 || got.StableID() != "17" || got.Title != "Terminal" || got.AppID != "org.gnome.Terminal" ||
		got.Class != "Terminal" || got.PID != 42 || got.X != 1 || got.Y != 2 || got.W != 800 || got.H != 600 ||
		!got.Active || got.Minimized || !got.Maximized || !got.Fullscreen {
		t.Fatalf("toWindowInfo = %#v", got)
	}
}

func TestGnomeNativeManagerNilReceiverReturnsErrors(t *testing.T) {
	var manager *GnomeNativeManager
	if _, err := manager.List(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("List error = %v, want initialization error", err)
	}
	if _, err := manager.ActiveTitle(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("ActiveTitle error = %v, want initialization error", err)
	}
	if err := manager.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("Sync error = %v, want initialization error", err)
	}
	if _, err := manager.InfoByID(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("InfoByID error = %v, want initialization error", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("nil Close error = %v, want nil", err)
	}
}

func TestGnomeNativeManagerCloseHandlesPartialConstruction(t *testing.T) {
	manager := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	if err := manager.Close(); err != nil {
		t.Fatalf("partial Close error = %v, want nil", err)
	}
}

func TestGnomeNativeManagerCloseEndsLiveEventStream(t *testing.T) {
	bridgeEvents := make(chan gnomebridge.WindowEvent)
	released := make(chan struct{})
	manager := &GnomeNativeManager{
		bridge:     &gnomebridge.Client{},
		events:     make(chan Event, 4),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go manager.forwardWindowEvents(bridgeEvents, func() { close(released) })

	select {
	case bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "17"}}:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge stream not accepted while the manager is live")
	}
	select {
	case event, ok := <-manager.WindowEvents():
		if !ok {
			t.Fatal("event stream closed while the bridge stream is still open")
		}
		if event.Kind != WindowAddedEvent || event.ID != "17" {
			t.Fatalf("forwarded event = %+v, want window-added for 17", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event forwarded while the bridge stream is still open")
	}

	// The bridge stream is deliberately still open: the manager owns the end
	// of its own event stream and must not depend on the bridge going away.
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish while the bridge event stream stayed open")
	}

	select {
	case <-released:
	default:
		t.Fatal("bridge subscription was not released")
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("event stream still open after Close")
	}
}

func TestToWindowInfoRetainsOpaqueID(t *testing.T) {
	got := toWindowInfo(gnomebridge.WindowInfo{ID: "opaque-id"})
	if got.ID != 0 || got.StableID() != "opaque-id" {
		t.Fatalf("toWindowInfo opaque ID = %#v", got)
	}
}

func TestValidateGnomeInt32ValuesRejectsOverflow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value int
	}{
		{name: "above", value: int(int64(1 << 31))},
		{name: "below", value: int(-int64(1<<31) - 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGnomeInt32Values("move", tc.value); err == nil {
				t.Fatal("value outside int32 range was accepted")
			}
		})
	}
}

func TestForwardWindowEventsConvertsAllNativeKinds(t *testing.T) {
	bridgeEvents := make(chan gnomebridge.WindowEvent, 4)
	m := &GnomeNativeManager{
		events:     make(chan Event, 4),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go m.forwardWindowEvents(bridgeEvents, func() {})
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "17", Title: "Terminal"}}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "17", Title: "Shell"}}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowRemovedEvent, ID: "17"}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.FocusChangedEvent, ID: "17"}
	close(bridgeEvents)

	var got []Event
	for event := range m.events {
		got = append(got, event)
	}
	want := []Event{
		{Kind: WindowAddedEvent, ID: "17", Window: Info{ID: 17, NativeID: "17", Title: "Terminal"}},
		{Kind: WindowChangedEvent, ID: "17", Window: Info{ID: 17, NativeID: "17", Title: "Shell"}},
		{Kind: WindowRemovedEvent, ID: "17"},
		{Kind: FocusChangedEvent, ID: "17"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("forwarded events = %#v, want %#v", got, want)
	}
}
