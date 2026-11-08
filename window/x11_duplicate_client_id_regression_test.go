//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// _NET_CLIENT_LIST is a set of managed windows, not an instruction to
// process a window multiple times if a faulty WM repeats its identifier.
func TestX11IterateWindowsDeduplicatesRepeatedClientIDs(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	old := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, kind xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 12)
			for i, id := range []uint32{50, 51, 50} {
				binary.LittleEndian.PutUint32(data[i*4:], id)
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return old(del, w, p, kind, offset, length)
	}
	windows, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(windows) != 2 || windows[0].ID != 50 || windows[1].ID != 51 {
		t.Fatalf("duplicate client IDs were emitted: %+v", windows)
	}
}
