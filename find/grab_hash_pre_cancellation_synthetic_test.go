package find

import (
	"context"
	"errors"
	"image"
	"testing"
)

// Hashing is an observation operation. A canceled caller must not authorize a
// framebuffer read, regardless of whether the backend provides a canonical
// zero-copy path or an ordinary image grab.
func TestGrabHashNeverTouchesBackendAfterCallerPreCanceled(t *testing.T) {
	rect := image.Rect(1, 1, 3, 3)
	for _, tc := range []struct {
		name      string
		rect      image.Rectangle
		canonical bool
	}{
		{"canonical region", rect, true},
		{"canonical full screen", image.Rectangle{}, true},
		{"regular image grab", rect, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if tc.canonical {
				capture := &canonicalHashScreenshotter{img: testHashImageRGBA()}
				h, err := GrabHash(ctx, capture, tc.rect, nil)
				if !errors.Is(err, context.Canceled) || h != 0 || capture.grabCalls != 0 || capture.regionCalls != 0 || capture.fullCalls != 0 {
					t.Fatalf("canceled hash accessed framebuffer: hash=%08x err=%v grabs=%d region=%d full=%d", h, err, capture.grabCalls, capture.regionCalls, capture.fullCalls)
				}
			} else {
				capture := &pixelInspectionScreenshotter{img: testHashImageRGBA()}
				h, err := GrabHash(ctx, capture, tc.rect, nil)
				if !errors.Is(err, context.Canceled) || h != 0 || capture.grabs != 0 {
					t.Fatalf("canceled hash captured pixels: hash=%08x err=%v grabs=%d", h, err, capture.grabs)
				}
			}
		})
	}
}
