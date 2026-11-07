//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// _NET_CLIENT_LIST can exceed the first GetProperty chunk. The server signals
// remaining elements through BytesAfter; silently returning only the first
// chunk produces an incomplete snapshot and hides windows.
func TestX11ClientListReadsEveryPropertyChunk(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Sample")
	original := conn.GetPropertyFunc
	var offsets []uint32
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, kind xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return original(del, w, p, kind, offset, length)
		}
		offsets = append(offsets, offset)
		if offset == 0 {
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
		}
		if offset == 1 {
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 51)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		t.Fatalf("unexpected property offset %d", offset)
		return nil
	}
	infos, err := b.List(context.Background())
	if err != nil || len(infos) != 2 || infos[0].ID != 50 || infos[1].ID != 51 {
		t.Fatalf("incomplete client list: infos=%v err=%v offsets=%v", infos, err, offsets)
	}
}

// A server that reports remaining bytes without returning any payload must
// fail instead of looping indefinitely or reporting an incomplete list.
func TestX11ClientListRejectsNonProgressingPropertyChunks(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 2 {
			t.Fatal("pagination looped without progress")
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, BytesAfter: 4})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 {
		t.Fatalf("non-progressing client list accepted: infos=%v err=%v", infos, err)
	}
}
