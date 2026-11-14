package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type cancelDuringPixelRead struct {
	bounds image.Rectangle
	at     func()
	calls  int
}

func (i *cancelDuringPixelRead) Bounds() image.Rectangle { return i.bounds }
func (i *cancelDuringPixelRead) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPixelRead) At(int, int) color.Color {
	i.calls++
	if i.calls == 1 && i.at != nil {
		i.at()
	}
	return color.RGBA{R: 99, G: 42, B: 18, A: 255}
}

// An untrusted backend can return a lazy image whose pixels are not retrieved
// until callers ask for them. Cancellation during the first read must prohibit
// subsequent reads and keep the partially read image out of the result.
func TestScreenMultiplePixelsStopsReadingAfterCancellationWithinImage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frame := &cancelDuringPixelRead{bounds: image.Rect(0, 0, 2, 1), at: cancel}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	pixels, err := session.Screen.GetMultiplePixels(ctx, []image.Point{{0, 0}, {1, 0}})
	if !errors.Is(err, context.Canceled) || pixels != nil || frame.calls != 1 {
		t.Fatalf("canceled pixel read disclosed result: pixels=%v err=%v calls=%d", pixels, err, frame.calls)
	}
}

// Session.Close after image capture withdraws the ability to expose pixels
// from that image, even when the original request context remains active.
func TestScreenMultiplePixelsStopsReadingAfterSessionCloseWithinImage(t *testing.T) {
	frame := &cancelDuringPixelRead{bounds: image.Rect(0, 0, 2, 1)}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	frame.at = func() {
		if err := session.Close(); err != nil {
			t.Errorf("session close: %v", err)
		}
	}
	pixels, err := session.Screen.GetMultiplePixels(context.Background(), []image.Point{{0, 0}, {1, 0}})
	if !errors.Is(err, ErrSessionClosed) || pixels != nil || frame.calls != 1 {
		t.Fatalf("closed session disclosed pixel data: pixels=%v err=%v calls=%d", pixels, err, frame.calls)
	}
}
