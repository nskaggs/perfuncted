package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

// A 2x desktop clips one logical pixel on both sides: the resulting pixel
// ratio is 1.8, but does not describe a valid capture of the requested area.
func TestMatchTemplateRejectsAmbiguousProportionallyClippedHiDPICapture(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 18, 18))
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	ref.SetRGBA(0, 0, color.RGBA{R: 253, G: 62, B: 91, A: 255})
	frame.SetRGBA(0, 0, color.RGBA{R: 253, G: 62, B: 91, A: 255})
	area := image.Rect(-1, -1, 9, 9)
	got, err := MatchTemplateInImage(context.Background(), frame, area, ref, 1)
	if err == nil || got != (TemplateMatch{}) {
		t.Fatalf("clipped capture returned unverifiable desktop coordinates %+v, err=%v", got, err)
	}
}

func TestMatchTemplateThresholdEqualityMustNotBePrunedByRounding(t *testing.T) {
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	reference.SetRGBA(0, 0, color.RGBA{R: 210, G: 180, B: 130, A: 255})
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 209, G: 180, B: 130, A: 255})
	confidence := 1 - float64(255)/float64(255*3*255)
	if math.IsNaN(confidence) || confidence <= 0 || confidence >= 1 {
		t.Fatal("invalid test confidence")
	}
	got, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, confidence)
	if err != nil || got.Rect != source.Bounds() || got.Confidence != confidence {
		t.Fatalf("score-equality candidate was incorrectly pruned: match=%+v, err=%v, threshold=%.17g", got, err, confidence)
	}
}

type wideCancelTemplateImage struct {
	ctxBounds image.Rectangle
	cancel    context.CancelFunc
	calls     int
}

func (img *wideCancelTemplateImage) ColorModel() color.Model { return color.RGBAModel }
func (img *wideCancelTemplateImage) Bounds() image.Rectangle { return img.ctxBounds }
func (img *wideCancelTemplateImage) At(x, y int) color.Color {
	img.calls++
	if img.calls == 10 {
		img.cancel()
	}
	return color.RGBA{R: 15, G: 20, B: 25, A: 255}
}

func TestMatchTemplateCancellationInspectsAtMostBoundedPixelsInSingleWideRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &wideCancelTemplateImage{ctxBounds: image.Rect(0, 0, 100000, 1), cancel: cancel}
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	ref.SetRGBA(0, 0, color.RGBA{R: 15, G: 20, B: 25, A: 255})
	got, err := MatchTemplateInImage(ctx, source, source.Bounds(), ref, 0.95)
	if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) {
		t.Fatalf("wide-row cancellation returned %+v, err=%v", got, err)
	}
	if source.calls > 5000 {
		t.Fatalf("read %d pixels after cancellation began; want at most 5000", source.calls)
	}
}

type templateBoundsWithoutPixels struct{ bounds image.Rectangle }

func (img templateBoundsWithoutPixels) ColorModel() color.Model { return color.RGBAModel }
func (img templateBoundsWithoutPixels) Bounds() image.Rectangle { return img.bounds }
func (templateBoundsWithoutPixels) At(int, int) color.Color {
	panic("pixel access must not happen for an oversized image")
}

func TestMatchTemplateRejectsOversizedCaptureAndReferenceBeforeReading(t *testing.T) {
	onePixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name      string
		source    image.Image
		reference image.Image
		area      image.Rectangle
	}{
		{
			name:      "capture over 16 Mi pixels",
			source:    templateBoundsWithoutPixels{image.Rect(0, 0, 4097, 4096)},
			reference: onePixel,
			area:      image.Rect(0, 0, 4097, 4096),
		},
		{
			name:      "reference over 2 Mi pixels",
			source:    templateBoundsWithoutPixels{image.Rect(0, 0, 2049, 1024)},
			reference: templateBoundsWithoutPixels{image.Rect(0, 0, 2049, 1024)},
			area:      image.Rect(0, 0, 2049, 1024),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchTemplateInImage(context.Background(), tc.source, tc.area, tc.reference, 0.95)
			if !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
				t.Fatalf("oversized image unexpectedly matched: %+v, err=%v", got, err)
			}
		})
	}
}
