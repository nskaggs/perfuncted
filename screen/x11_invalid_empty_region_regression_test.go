//go:build linux

package screen

import (
	"context"
	"image"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Only the zero rectangle requests the entire X11 display. An invalid region
// must fail without requesting private pixels from the X server.
func TestX11RejectsNonzeroEmptyRegionsWithoutGetImage(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			b, conn := newStubScreenX11Backend(t, false)
			b.screen.WidthInPixels = 2
			b.screen.HeightInPixels = 2
			called := 0
			conn.GetImageFunc = func(_ byte, _ xproto.Drawable, _, _ int16, _, _ uint16, _ uint32) x11.GetImageCookie {
				called++
				return x11.NewMockGetImageCookie(&xproto.GetImageReply{Data: make([]byte, 16)}, nil)
			}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) hash=%08x err=%v; want invalid region", rect, hash, err)
			}
			if called != 0 {
				t.Fatalf("invalid region dispatched %d XGetImage requests", called)
			}
		})
	}
}
