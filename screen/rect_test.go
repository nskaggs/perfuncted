package screen

import (
	"image"
	"math"
	"testing"
)

func TestLogicalRectToPhysical(t *testing.T) {
	rect := image.Rect(1, 2, 3, 4)

	if got := logicalRectToPhysical(rect, 1); got != rect {
		t.Fatalf("scale 1 = %v, want %v", got, rect)
	}

	want := image.Rect(2, 4, 6, 8)
	if got := logicalRectToPhysical(rect, 2); got != want {
		t.Fatalf("scale 2 = %v, want %v", got, want)
	}
}

func TestLogicalRectToPhysical_NonPositiveScale(t *testing.T) {
	rect := image.Rect(-2, -3, 4, 5)
	for _, scale := range []int{0, -1} {
		if got := logicalRectToPhysical(rect, scale); got != rect {
			t.Fatalf("scale %d = %v, want %v", scale, got, rect)
		}
	}
}

// Scaling must not move a rect to the other side of the coordinate space. A
// coordinate near the limit used to wrap negative, and because a rectangle is
// canonicalized, the wrapped maximum turned a rect lying far off to the right
// into one spanning the whole screen.
func TestLogicalRectToPhysicalDoesNotWrapAcrossTheScreen(t *testing.T) {
	frame := image.Rect(0, 0, 1920, 1080)

	cases := []struct {
		name string
		rect image.Rectangle
		// overlaps records whether the unscaled rect reaches the frame, which is
		// the property scaling has to preserve in both directions.
		overlaps bool
	}{
		{name: "whole screen at MaxInt", rect: image.Rect(0, 0, math.MaxInt, math.MaxInt), overlaps: true},
		{name: "whole screen from a negative origin", rect: image.Rect(-math.MaxInt, -math.MaxInt, math.MaxInt, math.MaxInt), overlaps: true},
		{name: "ordinary whole screen", rect: image.Rect(0, 0, 1920, 1080), overlaps: true},
		{name: "far right edge", rect: image.Rect(math.MaxInt-1, 0, math.MaxInt, 10), overlaps: false},
		{name: "far left edge", rect: image.Rect(-math.MaxInt, 0, -math.MaxInt+1, 10), overlaps: false},
		{name: "far right of a negative rect", rect: image.Rect(math.MaxInt-2, math.MaxInt-2, math.MaxInt, math.MaxInt), overlaps: false},
	}
	for _, scale := range []int{2, 3} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := logicalRectToPhysical(tc.rect, scale)
				if got.Min.X > got.Max.X || got.Min.Y > got.Max.Y {
					t.Fatalf("scaled rect is inverted: %v", got)
				}
				if got.Intersect(frame).Empty() == tc.overlaps {
					t.Fatalf(
						"scale %d: %v became %v, overlap with the frame changed from %v",
						scale, tc.rect, got, tc.overlaps,
					)
				}
			})
		}
	}
}

// Coordinates that do not overflow are scaled exactly as before.
func TestLogicalRectToPhysicalLeavesOrdinaryRectsExact(t *testing.T) {
	cases := []struct {
		rect  image.Rectangle
		scale int
		want  image.Rectangle
	}{
		{rect: image.Rect(1, 2, 3, 4), scale: 2, want: image.Rect(2, 4, 6, 8)},
		{rect: image.Rect(-3, -5, 7, 11), scale: 4, want: image.Rect(-12, -20, 28, 44)},
		{rect: image.Rect(0, 0, 1<<40, 1<<40), scale: 2, want: image.Rect(0, 0, 1<<41, 1<<41)},
		// Exactly at the limit the product still fits, so it is not saturated.
		{
			rect:  image.Rect(0, 0, math.MaxInt/2, math.MaxInt/2),
			scale: 2,
			want:  image.Rect(0, 0, math.MaxInt/2*2, math.MaxInt/2*2),
		},
	}
	for _, tc := range cases {
		if got := logicalRectToPhysical(tc.rect, tc.scale); got != tc.want {
			t.Fatalf("logicalRectToPhysical(%v, %d) = %v, want %v", tc.rect, tc.scale, got, tc.want)
		}
	}
}

// An enormous scale must not overflow the limit computation itself.
func TestScaleCoordHandlesEnormousScale(t *testing.T) {
	if got := scaleCoord(0, math.MaxInt); got != 0 {
		t.Fatalf("scaleCoord(0, MaxInt) = %d, want 0", got)
	}
	if got := scaleCoord(2, math.MaxInt); got != math.MaxInt {
		t.Fatalf("scaleCoord(2, MaxInt) = %d, want %d", got, math.MaxInt)
	}
	if got := scaleCoord(-2, math.MaxInt); got != -math.MaxInt {
		t.Fatalf("scaleCoord(-2, MaxInt) = %d, want %d", got, -math.MaxInt)
	}
	// Scaling is monotonic, so a rect never inverts.
	rect := image.Rect(-10, -10, 10, 10)
	got := logicalRectToPhysical(rect, math.MaxInt)
	if got.Min.X > got.Max.X || got.Min.Y > got.Max.Y {
		t.Fatalf("scaled rect is inverted: %v", got)
	}
}
