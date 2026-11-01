package find

import (
	"context"
	"errors"
	"image"
	"testing"
)

type cancelingCanonicalHashBackend struct {
	cancel       context.CancelFunc
	full, region int
}

func (b *cancelingCanonicalHashBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	panic("canonical hash backend must not Grab")
}
func (b *cancelingCanonicalHashBackend) CanonicalHashing() bool { return true }
func (b *cancelingCanonicalHashBackend) GrabFullHash(context.Context) (uint32, error) {
	b.full++
	b.cancel()
	return 0x12345678, nil
}
func (b *cancelingCanonicalHashBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	b.region++
	b.cancel()
	return 0x12345678, nil
}

func TestGrabHashCannotReturnSuccessAfterCancellationInsideCapture(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	for _, tc := range []struct {
		name string
		run  func(context.Context, context.CancelFunc) (uint32, error)
	}{
		{"image conversion", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			image := &cancelOnPixelImage{bounds: rect, cancel: cancel}
			return GrabHash(ctx, &pixelInspectionScreenshotter{img: image}, rect, nil)
		}},
		{"canonical region", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			b := &cancelingCanonicalHashBackend{cancel: cancel}
			h, err := GrabHash(ctx, b, rect, nil)
			if b.region != 1 || b.full != 0 {
				t.Errorf("wrong canonical path region=%d full=%d", b.region, b.full)
			}
			return h, err
		}},
		{"canonical full", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			b := &cancelingCanonicalHashBackend{cancel: cancel}
			h, err := GrabHash(ctx, b, image.Rectangle{}, nil)
			if b.region != 0 || b.full != 1 {
				t.Errorf("wrong canonical path region=%d full=%d", b.region, b.full)
			}
			return h, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hash, err := tc.run(ctx, cancel)
			if !errors.Is(err, context.Canceled) || hash != 0 {
				t.Fatalf("canceled private hash observation published: hash=%08x err=%v", hash, err)
			}
		})
	}
}
