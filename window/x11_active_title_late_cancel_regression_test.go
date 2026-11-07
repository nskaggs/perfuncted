//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ActiveTitleDoesNotPublishTitleAfterCancellationDuringMetadataRead(t *testing.T) {
	for _, cancelAt := range []string{"active window", "title"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, true, "Private title")
			old := conn.GetPropertyFunc
			conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, off, length uint32) x11.GetPropertyCookie {
				if (cancelAt == "active window" && p == b.atomNetActiveWindow) || (cancelAt == "title" && p == b.atomNetWMName) {
					cancel()
				}
				if p == b.atomNetActiveWindow {
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{50, 0, 0, 0}})
				}
				return old(del, w, p, typ, off, length)
			}
			title, err := b.ActiveTitle(ctx)
			if title != "" || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled active title disclosed title=%q err=%v", title, err)
			}
		})
	}
}
