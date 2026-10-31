package main

import (
	"context"
	"image"
	"image/color"
	"testing"
)

type cancelingPredicateImage struct {
	bounds image.Rectangle
	calls  int
	cancel context.CancelFunc
}

func (img *cancelingPredicateImage) ColorModel() color.Model { return color.RGBAModel }
func (img *cancelingPredicateImage) Bounds() image.Rectangle { return img.bounds }
func (img *cancelingPredicateImage) At(_, _ int) color.Color {
	img.calls++
	if img.cancel != nil && img.calls == 5 {
		img.cancel()
	}
	return color.RGBA{}
}

// The screen wait predicate is an application cancellation boundary. A long
// transparent screenshot must not continue scanning all remaining pixels
// after its wait was canceled, nor accept a successful match after cancel.
func TestScreenPredicateStopsScanningOnCancellation(t *testing.T) {
	for _, name := range []string{"non-empty", "non-zero", "opaque"} {
		t.Run(name, func(t *testing.T) {
			predicate, err := screenPredicate(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			img := &cancelingPredicateImage{bounds: image.Rect(0, 0, 900, 900), cancel: cancel}
			if predicate(ctx, img) {
				t.Fatal("empty image matched")
			}
			if img.calls > 900 {
				t.Fatalf("predicate continued after cancellation: %d pixel reads", img.calls)
			}
			cancel()
			img.calls = 0
			img.cancel = nil
			if predicate(ctx, img) {
				t.Fatal("canceled predicate matched")
			}
			if img.calls != 0 {
				t.Fatalf("predicate read %d pixels after pre-cancellation", img.calls)
			}
		})
	}
}

func TestScreenPredicateStillFindsVisiblePixelsWhenNotCanceled(t *testing.T) {
	for _, tc := range []struct {
		name string
		rgba color.RGBA
		want bool
	}{
		{"non-empty", color.RGBA{R: 50, A: 255}, true},
		{"non-zero", color.RGBA{R: 50, A: 255}, true},
		{"opaque", color.RGBA{R: 50, A: 255}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			predicate, err := screenPredicate(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(image.Rect(-3, -4, 4, 5))
			img.SetRGBA(3, 4, tc.rgba)
			if got := predicate(context.Background(), img); got != tc.want {
				t.Fatalf("visible pixel observed=%t want=%t", got, tc.want)
			}
		})
	}
}
