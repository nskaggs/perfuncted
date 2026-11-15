package find

import (
	"image"
	"image/color"
	"testing"
)

// Anchor recovery must score absolute differences instead of signed channel
// subtraction. A darker, distinctive leading pixel in an otherwise similar row must
// be selected ahead of lower-contrast windows after a candidate collision.
func TestLocateAnchorSelectsHighContrastDarkerReferenceWindow(t *testing.T) {
	bounds := image.Rect(0, 0, 5, 1)
	ref := image.NewRGBA(bounds)
	for x := 1; x < 5; x++ {
		ref.SetRGBA(x, 0, color.RGBA{R: 230, G: 230, B: 230, A: 255})
	}
	ref.SetRGBA(0, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	sourcePixel := []byte{240, 240, 240, 255}
	row, start, length := selectLocateAnchor(ref, bounds, bounds.Dx()*4, sourcePixel)
	if row != 0 || start != 1 || length != locateAnchorWindowBytes {
		t.Fatalf("dark contrast window not preferred: row=%d start=%d length=%d", row, start, length)
	}
}
