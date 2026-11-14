package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

// An explicit release in a typed sequence relinquishes the held modifier.
// When a later text operation fails, cleanup must not release that modifier
// again or retain phantom ownership. The same rule applies to a modifier
// previously held by an independent KeyDown call.
func TestGnomeTypeExplicitModifierReleaseDoesNotDoubleReleaseOnLaterFailure(t *testing.T) {
	failure := errors.New("GNOME text transport failed after modifier release")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, preheld := range []bool{false, true} {
		name := "acquired within call"
		if preheld {
			name = "held before call"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{textErr: failure}
			backend := &GnomeNativeBackend{bridge: bridge}
			ctx := context.Background()
			seq := "{ctrl down}{ctrl up}plain"
			if preheld {
				if err := backend.KeyDown(ctx, "ctrl"); err != nil {
					t.Fatal(err)
				}
				bridge.events = nil
				seq = "{ctrl up}plain"
			}
			err := backend.Type(ctx, seq)
			if !errors.Is(err, failure) {
				t.Fatalf("late text failure lost: %v", err)
			}
			if backend.held.any() {
				t.Fatalf("explicit release retained modifier ownership: %+v", backend.held)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("explicit modifier released %d times, want once: %v", got, bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != map[bool]int{false: 1, true: 0}[preheld] {
				t.Fatalf("unexpected modifier press count: %v", bridge.events)
			}
			bridge.events = nil
			bridge.textErr = nil
			if err := backend.Type(ctx, "{ctrl+a}"); err != nil {
				t.Fatalf("next independent shortcut failed: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 || countGnomeKeyEvents(bridge.events, "up", ctrl) != 1 {
				t.Fatalf("released modifier not reacquired by next shortcut: %v", bridge.events)
			}
		})
	}
}
