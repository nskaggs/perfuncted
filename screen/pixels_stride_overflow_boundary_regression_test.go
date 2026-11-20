package screen

import (
	"image"
	"image/color"
	"testing"
)

// A compositor may report a row stride close to MaxInt while providing only
// the first row of a frame. Decoding later rows must not overflow row offsets
// or turn absent pixels into fabricated opaque content.
func TestBGRADecodersRejectOverflowingLaterRowOffsets(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := []byte{9, 19, 29, 39}
	full := decodeBGRA(source, 1, 3, maxInt)
	if full.Bounds() != image.Rect(0, 0, 1, 3) {
		t.Fatalf("full frame bounds = %v", full.Bounds())
	}
	if got := full.RGBAAt(0, 0); got != (color.RGBA{R: 29, G: 19, B: 9, A: 255}) {
		t.Fatalf("available first row decoded incorrectly: %+v", got)
	}
	for y := 1; y < 3; y++ {
		if got := full.RGBAAt(0, y); got != (color.RGBA{}) {
			t.Fatalf("unavailable row %d fabricated pixels: %+v", y, got)
		}
	}

	// The second row's offset is (MaxInt + 4), which cannot be expressed in
	// an int. The rect decoder must stop before multiplying and adding it.
	region := decodeBGRARect(source, 2, 2, maxInt, image.Rect(1, 1, 2, 2))
	if region.Bounds() != image.Rect(1, 1, 2, 2) {
		t.Fatalf("subregion bounds = %v", region.Bounds())
	}
	if got := region.RGBAAt(1, 1); got != (color.RGBA{}) {
		t.Fatalf("overflowing source row yielded pixels: %+v", got)
	}
}
