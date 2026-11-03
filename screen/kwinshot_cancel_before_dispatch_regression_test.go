//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

func TestKWinCaptureCanceledBeforeDispatchDoesNotCallCompositor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &fakeKWinTransport{active: image.NewRGBA(image.Rect(0, 0, 2, 2)), area: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	b := &KWinShotBackend{transport: transport}
	for _, rect := range []image.Rectangle{{}, image.Rect(0, 0, 1, 1)} {
		img, err := b.Grab(ctx, rect)
		if img != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("Grab(%v) = %v, %v; want cancellation", rect, img, err)
		}
	}
	if transport.activeCalls != 0 || transport.areaCalls != 0 {
		t.Errorf("canceled captures dispatched to compositor: active=%d, area=%d", transport.activeCalls, transport.areaCalls)
	}
}
