//go:build linux
// +build linux

package screen

import (
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

func TestNormalizeGnomeCaptureMapsPixelsToLogicalGeometry(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), A: 0xff})
		}
	}

	got, err := normalizeGnomeCapture(src, gnomebridge.ScreenCapture{
		Width:       2,
		Height:      2,
		PixelWidth:  4,
		PixelHeight: 4,
		Scale:       2,
	})
	if err != nil {
		t.Fatalf("normalizeGnomeCapture: %v", err)
	}
	if got.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("normalized bounds = %v, want 2x2 origin", got.Bounds())
	}
	if got.At(1, 1) != (color.RGBA{R: 2, G: 2, B: 0, A: 0xff}) {
		t.Fatalf("normalized bottom-right pixel = %v, want source pixel (2,2)", got.At(1, 1))
	}
}

func TestNormalizeGnomeCaptureRejectsMetadataMismatch(t *testing.T) {
	_, err := normalizeGnomeCapture(
		image.NewRGBA(image.Rect(0, 0, 4, 4)),
		gnomebridge.ScreenCapture{
			Width:       2,
			Height:      2,
			PixelWidth:  3,
			PixelHeight: 4,
			Scale:       2,
		},
	)
	if err == nil {
		t.Fatal("normalizeGnomeCapture accepted mismatched PNG geometry")
	}
}
