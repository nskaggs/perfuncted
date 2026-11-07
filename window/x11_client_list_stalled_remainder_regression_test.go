//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A snapshot cannot be trusted when the remaining-byte count fails to
// decrease after consuming data. Otherwise a changing or broken property can
// force up to a million round trips while never yielding a consistent list.
func TestX11ClientListRejectsStalledRemainingBytes(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 2 {
			t.Fatal("stalled list continued requesting chunks")
		}
		if offset != uint32(calls-1) {
			t.Fatalf("wrong chunk offset %d at call %d", offset, calls)
		}
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(49+calls))
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 || !strings.Contains(err.Error(), "_NET_CLIENT_LIST") {
		t.Fatalf("stalled list accepted: infos=%v err=%v", infos, err)
	}
	if calls != 2 {
		t.Fatalf("expected detection after second chunk, got %d calls", calls)
	}
}
