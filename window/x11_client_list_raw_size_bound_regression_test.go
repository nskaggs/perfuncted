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

// Duplicate IDs must not evade the hard property-size budget. BytesAfter
// reveals the total raw size on the first reply even when the decoded list
// contains only one unique ID.
func TestX11ClientListRejectsOversizedRawPropertyBeforePagination(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 1 {
			t.Fatalf("oversized list performed %d requests", calls)
		}
		if offset != 0 {
			t.Fatalf("unexpected offset %d", offset)
		}
		v := make([]byte, 4)
		binary.LittleEndian.PutUint32(v, 50)
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4 * (1 << 20)})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 || !strings.Contains(err.Error(), "exceeds supported size") {
		t.Fatalf("oversized property not rejected: infos=%v err=%v calls=%d", infos, err, calls)
	}
	if calls != 1 {
		t.Fatalf("unexpected property calls %d", calls)
	}
}
