package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type nilImageScreenshotter struct{}

func (nilImageScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return nil, nil
}

func (nilImageScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (nilImageScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (nilImageScreenshotter) Close() error { return nil }

type emptyImageScreenshotter struct{}

func (emptyImageScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rectangle{}), nil
}

func (emptyImageScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (emptyImageScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func TestNilScreenshotterRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sc   Screenshotter
	}{
		{name: "nil interface", sc: nil},
		{name: "typed nil", sc: Screenshotter((*solidScreenshotter)(nil))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := GrabHash(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), nil); err == nil {
				t.Fatal("GrabHash succeeded unexpectedly")
			}
			if _, err := WaitForFn(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), func(context.Context, image.Image) bool { return true }, time.Millisecond); err == nil {
				t.Fatal("WaitForFn succeeded unexpectedly")
			}
		})
	}
}

func TestNilImageFromGrabRejected(t *testing.T) {
	sc := nilImageScreenshotter{}
	ctx := context.Background()
	rect := image.Rect(0, 0, 2, 2)
	reference := image.NewRGBA(rect)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "GrabHash",
			call: func() error {
				_, err := GrabHash(ctx, sc, rect, DefaultHasher)
				return err
			},
		},
		{
			name: "FirstPixel",
			call: func() error {
				_, err := FirstPixel(ctx, sc, rect)
				return err
			},
		},
		{
			name: "LocateExact",
			call: func() error {
				_, err := LocateExact(ctx, sc, rect, reference)
				return err
			},
		},
		{
			name: "FindColor",
			call: func() error {
				_, err := FindColor(ctx, sc, rect, color.RGBA{}, 0)
				return err
			},
		},
		{
			name: "ScanFor",
			call: func() error {
				_, err := ScanFor(ctx, sc, []image.Rectangle{rect}, []uint32{0}, time.Millisecond, DefaultHasher)
				return err
			},
		},
		{
			name: "WaitForFn",
			call: func() error {
				_, err := WaitForFn(ctx, sc, rect, func(context.Context, image.Image) bool { return true }, time.Millisecond)
				return err
			},
		},
		{
			name: "WaitForNoChangeFrom",
			call: func() error {
				_, err := WaitForNoChangeFrom(ctx, sc, rect, 0, 1, time.Millisecond, DefaultHasher)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("operation succeeded with nil image")
			} else if errors.Is(err, context.Canceled) {
				t.Fatalf("operation returned cancellation for nil image: %v", err)
			}
		})
	}

	if got := PixelHash(nil, nil); got != 0 {
		t.Fatalf("PixelHash(nil) = %d, want safe zero hash", got)
	}
}

func TestEmptyImageFromGrabRejectedByWaitForNoChange(t *testing.T) {
	_, err := WaitForNoChangeFrom(
		context.Background(),
		emptyImageScreenshotter{},
		image.Rect(0, 0, 2, 2),
		0,
		1,
		time.Millisecond,
		DefaultHasher,
	)
	if err == nil {
		t.Fatal("WaitForNoChangeFrom succeeded with an empty image")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNoChangeFrom returned cancellation for empty image: %v", err)
	}
}
