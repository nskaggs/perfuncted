package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

type genericTemplateImage struct{ image.Image }

func (g genericTemplateImage) ColorModel() color.Model { return g.Image.ColorModel() }
func (g genericTemplateImage) Bounds() image.Rectangle { return g.Image.Bounds() }
func (g genericTemplateImage) At(x, y int) color.Color { return g.Image.At(x, y) }

func TestMatchTemplateGenericImagesAndNonzeroSubimageCoordinates(t *testing.T) {
	referencePixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	referencePixels.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
	referencePixels.SetNRGBA(1, 0, color.NRGBA{R: 80, G: 100, B: 120, A: 255})
	referencePixels.SetNRGBA(0, 1, color.NRGBA{R: 140, G: 160, B: 180, A: 255})
	referencePixels.SetNRGBA(1, 1, color.NRGBA{R: 200, G: 220, B: 240, A: 255})
	sourcePixels := image.NewNRGBA(image.Rect(10, 20, 16, 24))
	for y := sourcePixels.Bounds().Min.Y; y < sourcePixels.Bounds().Max.Y; y++ {
		for x := sourcePixels.Bounds().Min.X; x < sourcePixels.Bounds().Max.X; x++ {
			sourcePixels.SetNRGBA(x, y, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			sourcePixels.Set(12+dx, 21+dy, referencePixels.At(dx, dy))
		}
	}
	// A wrapper keeps both images on the generic image.Image path instead of
	// allowing packed RGBA/NRGBA special cases to satisfy this test.
	source := genericTemplateImage{Image: sourcePixels}
	reference := genericTemplateImage{Image: referencePixels}
	area := image.Rect(-30, 40, -24, 44)
	got, err := MatchTemplateInImage(context.Background(), source, area, reference, 1)
	if err != nil {
		t.Fatalf("MatchTemplateInImage generic images: %v", err)
	}
	if want := image.Rect(-28, 41, -26, 43); got.Rect != want || got.Confidence != 1 {
		t.Fatalf("generic nonzero-origin result = %+v, want %v at 1", got, want)
	}
}

func TestMatchTemplateSemiTransparentReferenceWeightsPixelError(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	reference.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	reference.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	source.SetRGBA(1, 0, color.RGBA{G: 205, A: 255})
	got, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, 0.97)
	if err != nil {
		t.Fatalf("semi-transparent weighted match: %v", err)
	}
	want := 1 - float64(128*50)/float64((255+128)*3*255)
	if got.Rect != source.Bounds() || math.Abs(got.Confidence-want) > 1e-12 {
		t.Fatalf("semi-transparent score = %+v, want confidence %.12f", got, want)
	}
}

type boundsOnlyTemplateImage struct{ bounds image.Rectangle }

func (g boundsOnlyTemplateImage) ColorModel() color.Model { return color.RGBAModel }
func (g boundsOnlyTemplateImage) Bounds() image.Rectangle { return g.bounds }
func (boundsOnlyTemplateImage) At(int, int) color.Color {
	panic("bounds-only image pixels must not be read")
}

func TestMatchTemplateRejectsOverflowingGeometryBeforeReadingPixels(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	reference := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	for _, bounds := range []image.Rectangle{
		image.Rect(0, 0, maxInt, 2),
		image.Rect(minInt, 0, maxInt, 1),
	} {
		t.Run(bounds.String(), func(t *testing.T) {
			source := boundsOnlyTemplateImage{bounds: bounds}
			got, err := MatchTemplateInImage(context.Background(), source, bounds, reference, 0.95)
			if !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
				t.Fatalf("overflowing bounds returned %+v, %v; want invalid frame", got, err)
			}
		})
	}
}
