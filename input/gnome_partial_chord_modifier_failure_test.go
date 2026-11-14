package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

// A chord acquires its temporary modifiers one at a time. Failure to acquire
// a later modifier must release only the modifiers acquired earlier, preserve
// failures during release, and never dispatch the chord's primary key.
func TestGnomeChordSecondModifierFailureRollsBackFirstModifier(t *testing.T) {
	pressErr := errors.New("failed to press second modifier")
	cleanupErr := errors.New("failed to release first modifier")
	shift := gnomeSpecialKeyvals[keymap.KeyShift]
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, failCleanup := range []bool{false, true} {
		name := "rollback succeeds"
		if failCleanup {
			name = "rollback also fails"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			bridge.failOn = func(state string, keyval uint32) error {
				if state == "down" && keyval == ctrl {
					return pressErr
				}
				if failCleanup && state == "up" && keyval == shift {
					return cleanupErr
				}
				return nil
			}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), "{shift+ctrl+a}")
			if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != failCleanup {
				t.Fatalf("partial chord errors lost: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", shift) != 1 || countGnomeKeyEvents(bridge.events, "up", shift) != 1 || countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 || countGnomeKeyEvents(bridge.events, "down", uint32('a')) != 0 {
				t.Fatalf("partial chord crossed failed modifier or lost rollback: %v", bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("partial chord changed caller-held modifier state: %+v", backend.held)
			}
			bridge.events = nil
			bridge.failOn = nil
			if err := backend.Type(context.Background(), "{shift+ctrl+a}"); err != nil {
				t.Fatalf("next independent chord rejected: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", shift) != 1 || countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 {
				t.Fatalf("retry did not reacquire both modifiers: %v", bridge.events)
			}
		})
	}
}
