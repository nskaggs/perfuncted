package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/util"
)

// All visual reads must fail before attempting a capture when the backend is
// unavailable. Each operation has a distinct authorization entrypoint.
func TestVisualQueriesRejectUnavailableScreenshotterBeforeObservation(t *testing.T) {
	bounds := image.Rect(0, 0, 2, 2)
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"first pixel", func() error { _, e := FirstPixel(context.Background(), nil, bounds); return e }},
		{"wait for hash", func() error { _, e := WaitFor(context.Background(), nil, bounds, 1, time.Second, nil); return e }},
		{"wait for change", func() error { _, e := WaitForChange(context.Background(), nil, bounds, 1, time.Second, nil); return e }},
		{"wait for stable", func() error {
			_, e := WaitForNoChange(context.Background(), nil, bounds, 2, time.Second, nil)
			return e
		}},
		{"wait from known frame", func() error {
			_, e := WaitForNoChangeFrom(context.Background(), nil, bounds, 1, 2, time.Second, nil)
			return e
		}},
		{"scan", func() error {
			_, e := ScanFor(context.Background(), nil, []image.Rectangle{bounds}, []uint32{1}, time.Second, nil)
			return e
		}},
		{"locate exact", func() error { _, e := LocateExact(context.Background(), nil, bounds, ref); return e }},
		{"find color", func() error { _, e := FindColor(context.Background(), nil, bounds, color.RGBA{A: 255}, 0); return e }},
		{"wait locate", func() error { _, e := WaitForLocate(context.Background(), nil, bounds, ref, time.Second); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, util.ErrNotAvailable) {
				t.Fatalf("missing screen result: %v", err)
			}
		})
	}
}

type cancelingFindScreen struct {
	image  image.Image
	cancel context.CancelFunc
	grabs  int
}

func (s *cancelingFindScreen) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	s.grabs++
	s.cancel()
	return s.image, nil
}

func TestVisualQueriesDropCaptureWhenCallerRevokesWhileGrabReturns(t *testing.T) {
	rect := image.Rect(0, 0, 2, 2)
	picture := image.NewRGBA(rect)
	for _, tc := range []struct {
		name string
		run  func(context.Context, *cancelingFindScreen) error
	}{
		{"first pixel", func(c context.Context, s *cancelingFindScreen) error { _, e := FirstPixel(c, s, rect); return e }},
		{"find color", func(c context.Context, s *cancelingFindScreen) error {
			_, e := FindColor(c, s, rect, color.RGBA{A: 255}, 0)
			return e
		}},
		{"locate exact", func(c context.Context, s *cancelingFindScreen) error {
			_, e := LocateExact(c, s, rect, image.NewRGBA(image.Rect(0, 0, 1, 1)))
			return e
		}},
		{"scan", func(c context.Context, s *cancelingFindScreen) error {
			_, e := ScanFor(c, s, []image.Rectangle{rect}, []uint32{PixelHash(picture, nil)}, time.Second, nil)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			screen := &cancelingFindScreen{image: picture, cancel: cancel}
			err := tc.run(ctx, screen)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled source exposed pixels: %v", err)
			}
			if screen.grabs != 1 {
				t.Fatalf("invalid capture count=%d", screen.grabs)
			}
		})
	}
}

func TestFindPollingAndStableStreakFailClosedAtCancellationBoundary(t *testing.T) {
	var nilCtx context.Context
	if err := contextErr(nilCtx); err != nil {
		t.Fatalf("nil internal context not normalized: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	value, err := poll(ctx, 0, 17, func(int) (bool, uint32, error) { count++; cancel(); return true, 83, nil })
	if !errors.Is(err, context.Canceled) || value != 83 || count != 1 {
		t.Fatalf("adaptive post-poll cancellation: result=%d err=%v count=%d", value, err, count)
	}
	streak := newStableStreak(0, 0)
	if streak.stable != 1 {
		t.Fatalf("non-positive stable streak not clamped: %+v", streak)
	}
	empty := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	_, err = WaitForNoChange(nilCtx, empty, image.Rectangle{}, 0, 0, nil)
	if err == nil {
		t.Fatal("empty-region stable observer unexpectedly succeeded")
	}
}
