//go:build linux
// +build linux

package window

import (
	"slices"
	"testing"

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

func TestToWindowInfoRetainsOpaqueID(t *testing.T) {
	got := toWindowInfo(gnomebridge.WindowInfo{ID: "opaque-id"})
	if got.ID != 0 || got.StableID() != "opaque-id" {
		t.Fatalf("toWindowInfo opaque ID = %#v", got)
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
