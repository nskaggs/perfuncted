//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11IteratorStopsWhenClientListReplyCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
		cancel()
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
	}
	windows, err := b.List(ctx)
	if !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled client-list read reported success: windows=%#v err=%v", windows, err)
	}
}

func TestX11IteratorDoesNotPublishWindowAfterMetadataReadCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, true, "private-X11-title")
	original := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, off, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMName {
			cancel()
		}
		return original(del, w, p, tp, off, count)
	}
	windows, err := b.List(ctx)
	if !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled metadata read disclosed private window: windows=%#v err=%v", windows, err)
	}
}
