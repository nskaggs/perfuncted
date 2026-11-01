package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

// A synthetic generic image cancels the caller when the first pixel is read.
// This models a slow image adapter that finishes a read only after a session
// was revoked; the image data must not be published as a successful result.
type cancelOnPixelImage struct {
	bounds image.Rectangle
	cancel context.CancelFunc
	reads  int
}

func (i *cancelOnPixelImage) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelOnPixelImage) Bounds() image.Rectangle { return i.bounds }
func (i *cancelOnPixelImage) At(x, y int) color.Color {
	i.reads++
	if i.cancel != nil {
		i.cancel()
		i.cancel = nil
	}
	return color.RGBA{R: 220, G: 110, B: 45, A: 255}
}

type pixelInspectionScreenshotter struct {
	img   image.Image
	grabs int
}

func (s *pixelInspectionScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.img, nil
}

func TestFindPixelInspectionRejectsCancellationDuringActualPixelRead(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	pixel := color.RGBA{R: 220, G: 110, B: 45, A: 255}
	for _, tc := range []struct {
		name string
		run  func(context.Context, Screenshotter) (any, error)
	}{
		{"first pixel", func(ctx context.Context, sc Screenshotter) (any, error) {
			p, err := FirstPixel(ctx, sc, rect)
			return p, err
		}},
		{"find color", func(ctx context.Context, sc Screenshotter) (any, error) {
			p, err := FindColor(ctx, sc, rect, pixel, 0)
			return p, err
		}},
		{"locate exact", func(ctx context.Context, sc Screenshotter) (any, error) {
			reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
			reference.Set(0, 0, pixel)
			p, err := LocateExact(ctx, sc, rect, reference)
			return p, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			img := &cancelOnPixelImage{bounds: rect, cancel: cancel}
			sc := &pixelInspectionScreenshotter{img: img}
			_, err := tc.run(ctx, sc)
			if img.reads == 0 || sc.grabs != 1 {
				t.Fatalf("did not exercise real pixel access, reads=%d grabs=%d", img.reads, sc.grabs)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late private pixel result was accepted after cancellation: %v", err)
			}
		})
	}
}

// Even a provider that disregards ctx must never be called for a query that
// the client already canceled before dispatch.
func TestFindPixelOperationsNeverGrabAfterPreCancellation(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	pixel := color.RGBA{R: 220, G: 110, B: 45, A: 255}
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	reference.Set(0, 0, pixel)
	for _, tc := range []struct {
		name string
		run  func(context.Context, Screenshotter) error
	}{
		{"first pixel", func(ctx context.Context, sc Screenshotter) error { _, err := FirstPixel(ctx, sc, rect); return err }},
		{"find color", func(ctx context.Context, sc Screenshotter) error {
			_, err := FindColor(ctx, sc, rect, pixel, 0)
			return err
		}},
		{"locate exact", func(ctx context.Context, sc Screenshotter) error {
			_, err := LocateExact(ctx, sc, rect, reference)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			sc := &pixelInspectionScreenshotter{img: reference}
			err := tc.run(ctx, sc)
			if !errors.Is(err, context.Canceled) || sc.grabs != 0 {
				t.Fatalf("canceled operation contacted screenshot backend: grabs=%d err=%v", sc.grabs, err)
			}
		})
	}
}
