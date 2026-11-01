//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A repeated down on an already held EIS button must not synthesize an
// additional press. That press can reset or terminate drag recognition in a
// compositor, and it is not an independent action by the caller.
func TestRemoteDesktopDuplicateMouseDownDoesNotSendExtraEISFrames(t *testing.T) {
	for _, button := range []int{1, 2, 3} {
		t.Run([]string{"left", "middle", "right"}[button-1], func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			ctx := context.Background()
			if err := b.MouseDown(ctx, button); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatal(err)
				}
			}
			err := b.MouseDown(ctx, button)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate mouse down accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			code, err := remoteButtonCode(button)
			if err != nil {
				t.Fatal(err)
			}
			if !b.buttons[code] || len(b.buttons) != 1 {
				t.Fatalf("duplicate down changed existing drag: %v", b.buttons)
			}
			if err := b.MouseUp(ctx, button); err != nil {
				t.Fatalf("original drag cannot release: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.MouseDown(ctx, button); err != nil {
				t.Fatalf("repress after release failed: %v", err)
			}
		})
	}
}
