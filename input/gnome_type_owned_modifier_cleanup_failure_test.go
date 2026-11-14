package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

// A typed sequence that raises a modifier before a later failing operation
// owns that modifier. Its failure cleanup must release it, even when the
// bridge reports a second failure while releasing, and must not leave a
// phantom hold that would affect a subsequent independent operation.
func TestGnomeTypeReleasesNewlyHeldModifierAfterLaterTextFailure(t *testing.T) {
	textFailure := errors.New("GNOME text command failed")
	cleanupFailure := errors.New("GNOME modifier cleanup failed")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, failCleanup := range []bool{false, true} {
		name := "cleanup succeeds"
		if failCleanup {
			name = "cleanup also fails"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{textErr: textFailure}
			if failCleanup {
				bridge.failOn = func(state string, keyval uint32) error {
					if state == "up" && keyval == ctrl {
						return cleanupFailure
					}
					return nil
				}
			}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), "{ctrl down}plain")
			if !errors.Is(err, textFailure) {
				t.Fatalf("lost original text failure: %v", err)
			}
			if failCleanup != errors.Is(err, cleanupFailure) {
				t.Fatalf("cleanup failure was incorrectly represented: %v", err)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != 1 {
				t.Fatalf("modifier down count %d, events=%v", got, bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("modifier cleanup count %d, events=%v", got, bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("failed Type retained phantom modifier: %+v", backend.held)
			}
			bridge.failOn = nil
			bridge.textErr = nil
			bridge.events = nil
			if err := backend.Type(context.Background(), "{ctrl+a}"); err != nil {
				t.Fatalf("independent next shortcut failed: %v", err)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != 1 {
				t.Fatalf("next shortcut failed to acquire its own modifier: %v", bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("next shortcut failed to release its own modifier: %v", bridge.events)
			}
		})
	}
}
