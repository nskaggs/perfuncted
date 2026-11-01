//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// An unheld button-up can end an independently held drag in the target
// compositor. The EIS writer must reject it before sending any wire bytes.
func TestRemoteDesktopNeverReleasesUnheldButtonOnEISWire(t *testing.T) {
	for _, button := range []int{1, 2, 3} {
		t.Run([]string{"left", "middle", "right"}[button-1], func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.MouseUp(context.Background(), button)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld button %d release accepted: %v", button, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.buttons) != 0 {
				t.Fatalf("invalid release changed held button set: %v", b.buttons)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("local validation revoked session: %v", err)
			}
			if err := b.MouseDown(context.Background(), button); err != nil {
				t.Fatalf("valid down rejected: %v", err)
			}
			if err := b.MouseUp(context.Background(), button); err != nil {
				t.Fatalf("valid up rejected: %v", err)
			}
			for i := 0; i < 4; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("valid button transition %d absent: %v", i, err)
				}
			}
			if len(b.buttons) != 0 {
				t.Fatalf("valid up retained button state: %v", b.buttons)
			}
		})
	}
}
