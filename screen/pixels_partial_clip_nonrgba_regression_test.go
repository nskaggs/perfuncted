package screen

import (
	"image"
	"image/color"
	"testing"
)

// Portal PNG decoders can produce NRGBA rather than RGBA. Cropping the same
// requested region must preserve its size and pixel mapping in either case,
// even when the requested region partly extends beyond the captured desktop.
func TestCropImageNonRGBAClippedRegionMatchesRGBACoordinates(t *testing.T) {
	bounds := image.Rect(0, 0, 3, 3)
	rgba := image.NewRGBA(bounds)
	nrgba := image.NewNRGBA(bounds)
	pixel := color.RGBA{R: 178, G: 45, B: 21, A: 255}
	rgba.SetRGBA(0, 0, pixel)
	nrgba.SetNRGBA(0, 0, color.NRGBA(pixel))
	wanted := image.Rect(-1, -1, 2, 2)
	reference := cropImage(rgba, wanted)
	actual := cropImage(nrgba, wanted)
	if actual.Bounds().Dx() != reference.Bounds().Dx() || actual.Bounds().Dy() != reference.Bounds().Dy() {
		t.Fatalf("clipped PNG variant changed crop dimensions: RGBA=%v NRGBA=%v", reference.Bounds(), actual.Bounds())
	}
	if got := color.RGBAModel.Convert(actual.At(actual.Bounds().Min.X+1, actual.Bounds().Min.Y+1)); got != pixel {
		t.Fatalf("clipped NRGBA pixel mapping=%v want %v", got, pixel)
	}
}
