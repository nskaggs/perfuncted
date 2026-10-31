package window

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11MoveRejectsUnrepresentableSignedWireCoordinatesBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"x greater than int16", 32768, 42}, {"y greater than int16", 42, 32768},
		{"x less than int16", -32769, 42}, {"y less than int16", 42, -32769},
		{"x wraps uint32", 1 << 33, 42}, {"negative x wraps uint32", -(1 << 33), 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, true, "A")
			mutated := 0
			conn.ConfigureWindowCheckedFunc = func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
				mutated++
				return &x11.MockCheckCookie{}
			}
			err := b.MoveByID(context.Background(), "50", tc.x, tc.y)
			if err == nil || mutated != 0 {
				t.Fatalf("invalid X11 coordinate %d,%d dispatched %d times: %v", tc.x, tc.y, mutated, err)
			}
		})
	}
}
func TestX11MoveAllowsSignedWireBoundaryCoordinates(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "A")
	var moves [][2]int
	conn.ConfigureWindowCheckedFunc = func(_ xproto.Window, _ uint16, values []uint32) x11.ConfigureWindowCookie {
		moves = append(moves, [2]int{int(int32(values[0])), int(int32(values[1]))})
		return &x11.MockCheckCookie{}
	}
	for _, tc := range [][2]int{{-32768, 32767}, {32767, -32768}, {0, 0}} {
		if err := b.MoveByID(context.Background(), "50", tc[0], tc[1]); err != nil {
			t.Fatalf("valid signed x11 coordinate %v rejected: %v", tc, err)
		}
	}
	if len(moves) != 3 || moves[0] != ([2]int{-32768, 32767}) || moves[1] != ([2]int{32767, -32768}) || moves[2] != ([2]int{0, 0}) {
		t.Fatalf("valid signed positions were not preserved: %v", moves)
	}
}
