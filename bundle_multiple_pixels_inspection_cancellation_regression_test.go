package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type cancelDuringPixelInspection struct {
	after  func()
	called bool
}

func (i *cancelDuringPixelInspection) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPixelInspection) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (i *cancelDuringPixelInspection) At(x, y int) color.Color {
	if !i.called {
		i.called = true
		i.after()
	}
	return color.RGBA{R: 10, G: 20, B: 30, A: 255}
}

// A capture can succeed before the image provider begins exposing pixels.
// Cancellation or Session.Close during inspection must discard the entire
// partial result, not return colors read after authorization ended.
func TestGetMultiplePixelsRejectsAuthorityLossDuringPixelInspection(t *testing.T) {
	for _, mode := range []string{"cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			img := &cancelDuringPixelInspection{}
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			if mode == "cancel" {
				img.after = cancel
			} else {
				img.after = func() { _ = session.Close() }
			}
			colors, err := session.Screen.GetMultiplePixels(ctx, []image.Point{{X: 0, Y: 0}, {X: 1, Y: 1}})
			expected := error(context.Canceled)
			if mode == "session-close" {
				expected = ErrSessionClosed
			}
			if !errors.Is(err, expected) || len(colors) != 0 || !img.called {
				t.Fatalf("pixel inspection with %s leaked colors=%v error=%v provider called=%v", mode, colors, err, img.called)
			}
		})
	}
}
