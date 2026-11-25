package screen_test

import (
	"image"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/screen"
)

// Exhaust every 3x3 change pattern, using padded storage, a nonzero
// origin, and hidden color bytes. Every changed pixel must be included
// exactly once; unchanged pixels must never be emitted.
func TestReviewDirtyRectExhaustivePatterns(t *testing.T) {
	bounds := image.Rect(-2, 5, 1, 8)
	for mask := 0; mask < 1<<9; mask++ {
		a := image.NewNRGBA(image.Rect(-4, 3, 4, 10)).SubImage(bounds).(*image.NRGBA)
		b := image.NewNRGBA(bounds)
		for i := 0; i < 9; i++ {
			if mask&(1<<i) != 0 {
				b.Pix[(i/3)*b.Stride+(i%3)*4] = 123
			}
		}
		rects, full, err := screen.DirtyRects(a, b, 9)
		if err != nil || full {
			t.Fatalf("mask=%09b: rects=%v full=%v err=%v", mask, rects, full, err)
		}
		var reconstructed int
		for _, r := range rects {
			if r.Empty() || !r.In(bounds) {
				t.Fatalf("mask=%09b invalid rectangle %v", mask, r)
			}
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					bit := 1 << ((y-bounds.Min.Y)*3 + x - bounds.Min.X)
					if reconstructed&bit != 0 {
						t.Fatalf("mask=%09b duplicate pixel (%d,%d)", mask, x, y)
					}
					reconstructed |= bit
				}
			}
		}
		if reconstructed != mask {
			t.Fatalf("mask=%09b reconstructed=%09b rects=%v", mask, reconstructed, rects)
		}
		again, full, err := screen.DirtyRects(a, b, 9)
		if err != nil || full || !reflect.DeepEqual(again, rects) {
			t.Fatalf("mask=%09b nondeterministic", mask)
		}
		for limit := 1; limit <= 9; limit++ {
			got, resync, err := screen.DirtyRects(a, b, limit)
			if err != nil || resync != (len(rects) > limit) || (resync && got != nil) || (!resync && !reflect.DeepEqual(got, rects)) {
				t.Fatalf("mask=%09b limit=%d got=%v resync=%v err=%v", mask, limit, got, resync, err)
			}
		}
	}
}
