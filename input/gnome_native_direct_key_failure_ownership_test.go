package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

// A bridge can reject either side of a key transition. Bookkeeping of caller-
// held modifiers must follow acknowledged events rather than optimistic state
// changes, so an unsuccessful release does not surrender the caller's hold.
func TestGnomeDirectModifierPressAndReleaseFailuresRetainOwnership(t *testing.T) {
	failure := errors.New("injected GNOME virtual key transport failure")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, stage := range []string{"press", "release"} {
		t.Run(stage, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			ctx := context.Background()
			if stage == "release" {
				if err := b.KeyDown(ctx, "ctrl"); err != nil {
					t.Fatal(err)
				}
				bridge.events = nil
			}
			bridge.failOn = func(state string, keyval uint32) error {
				if keyval == ctrl && ((stage == "press" && state == "down") || (stage == "release" && state == "up")) {
					return failure
				}
				return nil
			}
			var err error
			if stage == "press" {
				err = b.KeyDown(ctx, "ctrl")
			} else {
				err = b.KeyUp(ctx, "ctrl")
			}
			if !errors.Is(err, failure) {
				t.Fatalf("GNOME failure lost: %v", err)
			}
			if held := b.held.ctrl; held != (stage == "release") {
				t.Fatalf("failed %s changed held modifier ownership: held=%t", stage, held)
			}
			expected := "down"
			if stage == "release" {
				expected = "up"
			}
			if countGnomeKeyEvents(bridge.events, expected, ctrl) != 1 {
				t.Fatalf("failed %s did not reach bridge once: %v", stage, bridge.events)
			}
			bridge.failOn = nil
			if stage == "press" {
				if err := b.KeyDown(ctx, "ctrl"); err != nil || !b.held.ctrl {
					t.Fatalf("retry press did not acquire modifier: %v %+v", err, b.held)
				}
			} else {
				if err := b.KeyUp(ctx, "ctrl"); err != nil || b.held.ctrl {
					t.Fatalf("retry release did not relinquish modifier: %v %+v", err, b.held)
				}
			}
		})
	}
}
