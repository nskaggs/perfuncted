package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
	"time"
)

type faultControlledCapture struct {
	image   image.Image
	failure error
	grabs   []image.Rectangle
}

func (s *faultControlledCapture) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs = append(s.grabs, rect)
	if s.failure != nil {
		return nil, s.failure
	}
	return s.image, nil
}

// Every high-level visual query must preserve the backend's original error,
// stop after one denied capture, and never invent a successful pixel result.
func TestVisualSearchOperationsPreserveDeniedCaptureAndNeverRetry(t *testing.T) {
	denied := errors.New("synthetic screenshot permission revoked")
	rect := image.Rect(4, 6, 8, 10)
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name string
		call func(Screenshotter) error
	}{
		{"grab hash", func(s Screenshotter) error { _, e := GrabHash(context.Background(), s, rect, nil); return e }},
		{"first pixel", func(s Screenshotter) error { _, e := FirstPixel(context.Background(), s, rect); return e }},
		{"find color", func(s Screenshotter) error {
			_, e := FindColor(context.Background(), s, rect, color.RGBA{R: 12, A: 255}, 0)
			return e
		}},
		{"locate exact", func(s Screenshotter) error { _, e := LocateExact(context.Background(), s, rect, reference); return e }},
		{"wait exact hash", func(s Screenshotter) error {
			_, e := WaitFor(context.Background(), s, rect, 0, time.Hour, nil)
			return e
		}},
		{"wait change", func(s Screenshotter) error {
			_, e := WaitForChange(context.Background(), s, rect, 0, time.Hour, nil)
			return e
		}},
		{"wait settle", func(s Screenshotter) error {
			_, e := WaitForNoChangeFrom(context.Background(), s, rect, 0, 2, time.Hour, nil)
			return e
		}},
		{"scan", func(s Screenshotter) error {
			_, e := ScanFor(context.Background(), s, []image.Rectangle{rect}, []uint32{0}, time.Hour, nil)
			return e
		}},
		{"wait locate", func(s Screenshotter) error {
			_, e := WaitForLocate(context.Background(), s, rect, reference, time.Hour)
			return e
		}},
		{"wait predicate", func(s Screenshotter) error {
			_, e := WaitForFn(context.Background(), s, rect, func(context.Context, image.Image) bool { return true }, time.Hour)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &faultControlledCapture{failure: denied}
			err := tc.call(backend)
			if !errors.Is(err, denied) {
				t.Fatalf("provider failure not preserved: %v", err)
			}
			if len(backend.grabs) != 1 {
				t.Fatalf("provider failure triggered %d screenshots", len(backend.grabs))
			}
		})
	}
}

func TestVisualSearchMalformedGenericCaptureAndOverflowCoordinates(t *testing.T) {
	empty := &faultControlledCapture{image: syntheticImage{image.NewRGBA(image.Rectangle{})}}
	pixel, err := FirstPixel(context.Background(), empty, image.Rect(10, 12, 14, 16))
	if err == nil || pixel != (color.RGBA{}) || len(empty.grabs) != 1 {
		t.Fatalf("empty generic capture yielded pixels: %+v %v (grabs=%d)", pixel, err, len(empty.grabs))
	}
	invalid := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	for _, rect := range []image.Rectangle{
		{Min: image.Pt(math.MaxInt, 0), Max: image.Pt(math.MaxInt, 1)},
		{Min: image.Pt(0, math.MaxInt), Max: image.Pt(1, math.MaxInt)},
	} {
		_, err := FirstPixel(context.Background(), invalid, rect)
		if err == nil {
			t.Fatalf("overflowing first pixel coordinate accepted: %v", rect)
		}
	}
	if len(invalid.grabs) != 0 {
		t.Fatalf("overflowing coordinates caused capture: %+v", invalid.grabs)
	}
}

func TestVisualScanRejectsUnboundedUnionWithoutCapturingIt(t *testing.T) {
	// Untrusted source regions can overflow their width times height; the
	// scanner must choose per-region capture rather than overflow its union.
	rect := image.Rectangle{Min: image.Pt(-math.MaxInt/2, 0), Max: image.Pt(math.MaxInt/2, 4)}
	backend := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	_, err := ScanFor(context.Background(), backend, []image.Rectangle{rect}, []uint32{0}, time.Hour, nil)
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("unbounded region did not fail closed: %v", err)
	}
	if len(backend.grabs) != 1 || backend.grabs[0] != rect {
		t.Fatalf("unbounded scan used a different capture region: %+v", backend.grabs)
	}
}
