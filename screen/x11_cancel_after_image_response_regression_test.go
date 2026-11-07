//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// The X11 reply is synchronous. A caller can revoke image authority while
// the server is responding, and the completed reply must not be published.
func TestX11CaptureRejectsCancellationDuringImageReply(t *testing.T) {
	operations := []struct {
		name string
		run  func(*X11Backend, context.Context) error
	}{
		{"image", func(b *X11Backend, c context.Context) error {
			img, err := b.Grab(c, image.Rect(0, 0, 1, 1))
			if img != nil {
				t.Errorf("canceled image exposed pixels")
			}
			return err
		}},
		{"full hash", func(b *X11Backend, c context.Context) error {
			hash, err := b.GrabFullHash(c)
			if hash != 0 {
				t.Errorf("canceled hash exposed checksum %d", hash)
			}
			return err
		}},
		{"region hash", func(b *X11Backend, c context.Context) error {
			hash, err := b.GrabRegionHash(c, image.Rect(0, 0, 1, 1))
			if hash != 0 {
				t.Errorf("canceled region hash exposed checksum %d", hash)
			}
			return err
		}},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			b, conn := newStubScreenX11Backend(t, false)
			b.screen.WidthInPixels = 1
			b.screen.HeightInPixels = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
				cancel()
				return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Data: []byte{2, 4, 6, 8}}, nil)
			}
			if err := op.run(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled XGetImage returned %v", err)
			}
		})
	}
}
