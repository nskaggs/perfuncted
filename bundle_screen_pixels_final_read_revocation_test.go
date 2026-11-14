package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type revokeOnLastPixelImage struct {
	reads      int
	onLastRead func()
}

func (*revokeOnLastPixelImage) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 1) }
func (*revokeOnLastPixelImage) ColorModel() color.Model { return color.RGBAModel }
func (i *revokeOnLastPixelImage) At(int, int) color.Color {
	i.reads++
	if i.reads == 2 && i.onLastRead != nil {
		i.onLastRead()
	}
	return color.RGBA{R: 99, G: 42, B: 18, A: 255}
}

// The final pixel is still untrusted input. Cancellation or Session.Close
// during its At call must not publish an otherwise-complete private result.
// The post-loop check matters because there is no next iteration to enforce
// the per-pixel admission guard.
func TestScreenMultiplePixelsRejectsRevocationDuringFinalPixelRead(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelCaller bool
		want         error
	}{
		{name: "caller cancellation", cancelCaller: true, want: context.Canceled},
		{name: "session closure", want: ErrSessionClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frame := &revokeOnLastPixelImage{}
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			frame.onLastRead = func() {
				if tc.cancelCaller {
					cancel()
					return
				}
				if err := session.Close(); err != nil {
					t.Errorf("session close: %v", err)
				}
			}
			pixels, err := session.Screen.GetMultiplePixels(ctx, []image.Point{{0, 0}, {1, 0}})
			if pixels != nil || !errors.Is(err, tc.want) || frame.reads != 2 {
				t.Fatalf("revoked last read published data: pixels=%v err=%v reads=%d", pixels, err, frame.reads)
			}
		})
	}
}
