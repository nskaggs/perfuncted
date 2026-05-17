package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

type resolutionCancelScreenshotter struct {
	img    image.Image
	cancel context.CancelFunc
}

func (s *resolutionCancelScreenshotter) cancelOnce() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *resolutionCancelScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.cancelOnce()
	return s.img, nil
}

func (s *resolutionCancelScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return 0, nil
}

func (s *resolutionCancelScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return 0, nil
}

func (s *resolutionCancelScreenshotter) Close() error { return nil }

func TestResolutionWithContext_CanceledAfterGrab(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	img := image.NewRGBA(image.Rect(0, 0, 11, 7))
	sc := &resolutionCancelScreenshotter{img: img, cancel: cancel}

	w, h, err := ResolutionWithContext(ctx, sc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolutionWithContext error = %v, want context.Canceled", err)
	}
	if w != 0 || h != 0 {
		t.Fatalf("ResolutionWithContext returned %dx%d on cancellation, want 0x0", w, h)
	}
}

func TestResolutionWithContext_NilScreenshotter(t *testing.T) {
	var sc *resolutionCancelScreenshotter

	w, h, err := ResolutionWithContext(context.Background(), Screenshotter(sc))
	if err == nil {
		t.Fatal("expected error for nil screenshotter")
	}
	if w != 0 || h != 0 {
		t.Fatalf("ResolutionWithContext returned %dx%d for nil screenshotter, want 0x0", w, h)
	}
}
