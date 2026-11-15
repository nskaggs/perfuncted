package find

import (
	"context"
	"image"
	"image/color"
	"strings"
	"testing"
)

// Exact matching may receive untrusted RGBA planes with legal bounds but
// impossible buffer strides. Never fall through to a pixel comparison, which
// can panic or turn an incomplete screenshot into a false-positive match.
func TestExactPackedMatcherRejectsMalformedSourceAndReferenceLayout(t *testing.T) {
	bounds := image.Rect(0, 0, 2, 2)
	good := image.NewRGBA(bounds)
	good.SetRGBA(0, 0, color.RGBA{R: 19, A: 255})
	malformedSource := &image.RGBA{Pix: make([]byte, 16), Stride: 3, Rect: bounds}
	malformedReference := &image.RGBA{Pix: make([]byte, 16), Stride: 3, Rect: bounds}
	if matchAt(malformedSource, good, 0, 0) {
		t.Fatal("invalid capture stride accepted")
	}
	if matchAt(good, malformedReference, 0, 0) {
		t.Fatal("invalid reference stride accepted")
	}
	if !matchAt(good, good, 0, 0) {
		t.Fatal("valid packed match rejected")
	}
}

type genericEmptyFrameScreenshotter struct{ emptyImageScreenshotter }

func (genericEmptyFrameScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewGray(image.Rectangle{}), nil
}

// A compositor that sends an empty frame after a successful Grab cannot
// count that observation toward a no-change stability streak.
func TestNoChangeProbeRejectsEmptyGrabWithoutAdvancingStreak(t *testing.T) {
	streak := newStableStreak(0, 2)
	probe := grabHashProbe(context.Background(), genericEmptyFrameScreenshotter{}, image.Rect(0, 0, 2, 2), nil, &streak)
	done, hash, err := probe(0)
	if done || hash != 0 || err == nil || !strings.Contains(err.Error(), "empty image") {
		t.Fatalf("empty grab counted as stable: done=%v hash=%d err=%v", done, hash, err)
	}
}
