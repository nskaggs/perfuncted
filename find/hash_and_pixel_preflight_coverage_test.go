package find

import (
	"context"
	"errors"
	"image"
	"testing"
)

// An invalid rectangle or previously canceled hash operation must not
// trigger a capture. A nonempty rect cannot begin at MaxInt: there is no
// representable exclusive Max.X or Max.Y, so the existing Empty check is the
// only reachable preflight for such input.
func TestFindDirectHashAndPixelPreflightRejectBeforeCapture(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{name: "x coordinate overflow", rect: image.Rectangle{Min: image.Pt(maxInt, 0), Max: image.Pt(maxInt, 1)}},
		{name: "y coordinate overflow", rect: image.Rectangle{Min: image.Pt(0, maxInt), Max: image.Pt(1, maxInt)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 1, 1))}
			// Construct a nonempty rectangle without overflowing its coordinate;
			// an inverse edge is permitted at this trust boundary and the overflow
			// preflight must take priority over any attempt to grab the backend.
			rect := tc.rect
			if rect.Min.X == maxInt {
				rect.Max.X = maxInt
			} else {
				rect.Max.Y = maxInt
			}
			p, err := FirstPixel(context.Background(), backend, rect)
			if err == nil || p != (struct{ R, G, B, A uint8 }{}) || backend.grabs != 0 {
				t.Fatalf("unsafe pixel coordinate reached backend: pixel=%+v err=%v grabs=%d", p, err, backend.grabs)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	hash, err := grabImageHash(ctx, backend, image.Rect(0, 0, 2, 2), nil, false)
	if hash != 0 || !errors.Is(err, context.Canceled) || backend.grabs != 0 {
		t.Fatalf("pre-canceled direct image hash accessed capture: hash=%08x err=%v grabs=%d", hash, err, backend.grabs)
	}
}
