package screen

import (
	"image"
	"math"
	"testing"
)

// The requested area can have valid int coordinates yet exceed the number of
// addressable pixels. Reject it before allocating or indexing untrusted frame
// bytes, without requiring the test to allocate a large image.
func TestDecodeBGRARectRejectsOversizedPixelAreaBeforeAllocating(t *testing.T) {
	img := decodeBGRARect([]byte{0, 0, 0, 0}, 2, math.MaxInt, 8, image.Rect(0, 0, 2, math.MaxInt))
	if img == nil || !img.Bounds().Empty() || len(img.Pix) != 0 {
		t.Fatalf("oversized compositor rectangle accepted: bounds=%v bytes=%d", img.Bounds(), len(img.Pix))
	}
}
