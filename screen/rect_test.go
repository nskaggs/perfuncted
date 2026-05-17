package screen

import (
	"image"
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
