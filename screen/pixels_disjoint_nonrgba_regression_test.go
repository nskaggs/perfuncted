package screen

import (
	"image"
	"image/color"
	"testing"
)

// PNG-backed NRGBA and generic images must preserve crop size and transparent
// padding even when the requested rectangle is completely off-screen.
func TestCropImageDisjointNonRGBAPreservesRequestedGeometry(t *testing.T) {
	for _, wanted := range []image.Rectangle{image.Rect(8, 7, 12, 10), image.Rect(-8, -7, -4, -3)} {
		t.Run(wanted.String(), func(t *testing.T) {
			bounds := image.Rect(0, 0, 3, 3)
			rgba := image.NewRGBA(bounds)
			nrgba := image.NewNRGBA(bounds)
			rgba.SetRGBA(0, 0, color.RGBA{R: 90, A: 255})
			nrgba.SetNRGBA(0, 0, color.NRGBA{R: 90, A: 255})
			reference := cropImage(rgba, wanted)
			actual := cropImage(nrgba, wanted)
			if actual.Bounds() != reference.Bounds() {
				t.Fatalf("disjoint crop size mismatch: RGBA=%v NRGBA=%v", reference.Bounds(), actual.Bounds())
			}
			if r, g, b, a := actual.At(0, 0).RGBA(); r != 0 || g != 0 || b != 0 || a != 0 {
				t.Fatalf("disjoint crop pixel has nontransparent channels: %d %d %d %d", r, g, b, a)
			}
		})
	}
}
