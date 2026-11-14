package input

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// Reject invalid typed-input contexts before parsing or dispatch: otherwise
// a nil operation context could be mistaken for background authorization.
func TestRemoteDesktopTypeNilContextRejectsWithoutEISFrames(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	err := b.Type(nil, "private{enter}")
	if err == nil || !strings.Contains(err.Error(), "nil operation context") {
		t.Fatalf("nil context accepted: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
		t.Fatalf("rejected type modified input state: keys=%v buttons=%v pointer=%t", b.heldKeys, b.buttons, b.hasPointer)
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("local invalid context revoked healthy session: %v", err)
	}
}

// Scroll clicks are converted to EIS fixed-point deltas. Even a small click
// count must not permit a huge coordinate to wrap a signed 32-bit frame.
func TestRemoteDesktopScrollRejectsScaledCoordinateOverflowBeforeEISFrames(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"positive horizontal", math.MaxInt32/120 + 1, 0},
		{"negative horizontal", math.MinInt32/120 - 1, 0},
		{"positive vertical", 0, math.MaxInt32/120 + 1},
		{"negative vertical", 0, math.MinInt32/120 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.scroll(context.Background(), tc.x, tc.y, 1)
			if err == nil || !strings.Contains(err.Error(), "overflows") {
				t.Fatalf("out-of-range scroll accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
				t.Fatalf("rejected scroll modified state: keys=%v buttons=%v pointer=%t", b.heldKeys, b.buttons, b.hasPointer)
			}
			if err := b.stateError(); err != nil && !errors.Is(err, ErrRemoteDesktopUnavailable) {
				t.Fatalf("invalid scroll revoked authorized backend: %v", err)
			}
		})
	}
}
