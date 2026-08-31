//go:build linux
// +build linux

package window

import (
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
