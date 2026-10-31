package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type malformedCaptureBackend struct{ frame image.Image }

func (b malformedCaptureBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return b.frame, nil
}
func (b malformedCaptureBackend) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (b malformedCaptureBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (b malformedCaptureBackend) Close() error { return nil }

func TestUntrustedTruncatedCaptureNeverPanicsPixelInspection(t *testing.T) {
	rgba := &image.RGBA{Pix: []byte{1, 2}, Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	nrgba := &image.NRGBA{Pix: []byte{1, 2}, Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{{"RGBA", rgba}, {"NRGBA", nrgba}} {
		t.Run(tc.name, func(t *testing.T) {
			backend := malformedCaptureBackend{frame: tc.frame}
			if pixel, err := FirstPixel(context.Background(), backend, image.Rect(0, 0, 1, 1)); !errors.Is(err, ErrInvalidFrame) || pixel != (color.RGBA{}) {
				t.Fatalf("FirstPixel accepted truncated %s: %v %v", tc.name, pixel, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := WaitForNoChangeFrom(ctx, backend, image.Rect(0, 0, 1, 1), 0, 2, time.Millisecond, nil); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("settle loop accepted truncated %s: %v", tc.name, err)
			}
			if hash := PixelHash(tc.frame, nil); hash != 0 {
				t.Fatalf("truncated %s image hashed nonzero: %08x", tc.name, hash)
			}
		})
	}
}
