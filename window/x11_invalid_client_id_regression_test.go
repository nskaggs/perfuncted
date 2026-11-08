//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Window 0 is X11 None, not an addressable client. The window manager's
// client list must not advertise it as a real window or mark it active.
func TestX11IterateWindowsSkipsInvalidZeroClientID(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	prev := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			v := make([]byte, 8)
			binary.LittleEndian.PutUint32(v[4:], 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v})
		}
		return prev(del, w, p, typ, offset, length)
	}
	windows, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("window listing failed: %v", err)
	}
	if len(windows) != 1 || windows[0].ID != 50 {
		t.Fatalf("client list returned invalid window: %+v", windows)
	}
}
