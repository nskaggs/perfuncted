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

type resolutionResolver struct{}

type contextResolutionResolver struct{}

func (resolutionResolver) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 11, 7)), nil
}

func (resolutionResolver) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (resolutionResolver) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (resolutionResolver) Resolution() (int, int, error) { return 11, 7, nil }

func (resolutionResolver) Close() error { return nil }

func (contextResolutionResolver) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 17, 13)), nil
}

func (contextResolutionResolver) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (contextResolutionResolver) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (contextResolutionResolver) Resolution() (int, int, error) { return 1, 1, nil }

func (contextResolutionResolver) ResolutionWithContext(ctx context.Context) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return 17, 13, nil
}

func (contextResolutionResolver) Close() error { return nil }

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

func TestResolutionWithContext_CanceledResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := ResolutionWithContext(ctx, resolutionResolver{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolutionWithContext error = %v, want context.Canceled", err)
	}
}

func TestResolutionWithContext_PrefersContextAwareResolver(t *testing.T) {
	w, h, err := ResolutionWithContext(context.Background(), contextResolutionResolver{})
	if err != nil {
		t.Fatalf("ResolutionWithContext error = %v", err)
	}
	if w != 17 || h != 13 {
		t.Fatalf("ResolutionWithContext returned %dx%d, want 17x13", w, h)
	}
}
