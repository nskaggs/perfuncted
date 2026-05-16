package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type cancelOnGrabScreenshotter struct {
	img    image.Image
	cancel context.CancelFunc
	grabs  int
}

func (s *cancelOnGrabScreenshotter) cancelOnce() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *cancelOnGrabScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	s.cancelOnce()
	return s.img, nil
}

func (s *cancelOnGrabScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func (s *cancelOnGrabScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func TestWaitFor_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitFor(ctx, sc, image.Rect(0, 0, 4, 4), want, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFor error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitFor returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{G: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), 0, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForNoChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{B: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), want, 1, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNoChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForNoChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForFn_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 200, G: 100, B: 50, A: 255})
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForFn(ctx, sc, image.Rect(0, 0, 4, 4), func(context.Context, image.Image) bool {
		return true
	}, 10*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForFn error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("WaitForFn returned image on cancellation, want nil")
	}
}
