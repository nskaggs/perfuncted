//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

type lateCanceledKWinTransport struct {
	cancel                 context.CancelFunc
	activeCalls, areaCalls int
}

func (t *lateCanceledKWinTransport) CaptureActiveScreen(context.Context) (image.Image, error) {
	t.activeCalls++
	t.cancel()
	return image.NewRGBA(image.Rect(0, 0, 4, 3)), nil
}
func (t *lateCanceledKWinTransport) CaptureArea(context.Context, image.Rectangle) (image.Image, error) {
	t.areaCalls++
	t.cancel()
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}

// Successful compositor replies cannot override an operation canceled while
// capture was in flight, and the returned pixels must not escape to callers.
func TestKWinLateCancellationDiscardsCapturedPixels(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rect         image.Rectangle
		active, area int
	}{
		{"full screen", image.Rectangle{}, 1, 0},
		{"region", image.Rect(0, 0, 2, 2), 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := &lateCanceledKWinTransport{cancel: cancel}
			backend := &KWinShotBackend{transport: transport}
			img, err := backend.Grab(ctx, tc.rect)
			if img != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("late canceled capture: img=%v err=%v", img, err)
			}
			if transport.activeCalls != tc.active || transport.areaCalls != tc.area {
				t.Fatalf("dispatch count active=%d area=%d", transport.activeCalls, transport.areaCalls)
			}
		})
	}
}
