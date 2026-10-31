package screen

import (
	"context"
	"image"
	"math"
	"strings"
	"testing"
)

// An untrusted capture implementation can return an image.Image with a raw
// inverted rectangle or integer-wrapped dimensions. Both must be rejected;
// callers must never observe negative resolution or pass it to allocations.
func TestScreenResolutionFallbackRejectsNegativeOrOverflowedImageBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bounds image.Rectangle
	}{
		{"negative width", image.Rectangle{Min: image.Pt(8, 0), Max: image.Pt(3, 10)}},
		{"negative height", image.Rectangle{Min: image.Pt(0, 8), Max: image.Pt(10, 3)}},
		{"overflowed width", image.Rectangle{Min: image.Pt(math.MinInt, 0), Max: image.Pt(math.MaxInt, 10)}},
		{"overflowed height", image.Rectangle{Min: image.Pt(0, math.MinInt), Max: image.Pt(10, math.MaxInt)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &resolutionCancelScreenshotter{img: &image.RGBA{Rect: tc.bounds}}
			width, height, err := ResolutionWithContext(context.Background(), backend)
			if err == nil || width != 0 || height != 0 || !strings.Contains(err.Error(), "resolution probe") {
				t.Fatalf("malformed geometry %v reported %dx%d, err=%v", tc.bounds, width, height, err)
			}
		})
	}
}
