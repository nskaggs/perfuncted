package window

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestActiveTitle(t *testing.T) {
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.GetPropertyFunc = func(Delete bool, Window xproto.Window, Property, Type xproto.Atom, LongOffset, LongLength uint32) x11.GetPropertyCookie {
		// _NET_ACTIVE_WINDOW
		if Property == 1000 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte{2, 0, 0, 0}, Format: 32})
		}
		// _NET_WM_NAME
		if Property == 1001 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("My Window")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte{}})
	}

	b := NewX11BackendWithConn(mc)
	b.atomNetActiveWindow = xproto.Atom(1000)
	b.atomNetWMName = xproto.Atom(1001)
	b.atomUTF8String = xproto.Atom(0)

	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "My Window" {
		t.Fatalf("expected 'My Window', got %q", title)
	}
}
