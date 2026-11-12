//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// An X server is permitted to close or malfunction while the client list is
// read. Neither a nil reply nor canceled pagination can publish an incomplete
// window list as if it were authoritative.
func TestX11ClientListNilPropertyReplyNeverPublishesWindows(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			t.Fatalf("queried unrelated property %d", property)
		}
		calls++
		return x11.NewMockGetPropertyCookie(nil)
	}
	windows, err := b.List(context.Background())
	if len(windows) != 0 || err == nil || !strings.Contains(err.Error(), "nil _NET_CLIENT_LIST reply") {
		t.Fatalf("nil property reply published windows=%+v err=%v", windows, err)
	}
	if calls != 1 {
		t.Fatalf("nil reply retried %d times", calls)
	}
}

func TestX11ClientListCanceledDuringSecondPageDiscardsFirstPage(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			t.Fatalf("queried unrelated property %d", property)
		}
		calls++
		if calls > 2 {
			t.Fatalf("canceled enumeration continued to call %d", calls)
		}
		if calls == 1 {
			if offset != 0 {
				t.Fatalf("first page began at offset %d", offset)
			}
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
		}
		if offset != 1 {
			t.Fatalf("second page offset %d", offset)
		}
		cancel() // transport finishes but authorization expired before accepting the reply
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, 51)
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
	}
	windows, err := b.List(ctx)
	if len(windows) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled paginated list published windows=%+v err=%v", windows, err)
	}
	if calls != 2 {
		t.Fatalf("canceled pagination made %d property requests", calls)
	}
}
