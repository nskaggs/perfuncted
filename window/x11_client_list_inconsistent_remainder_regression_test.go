//go:build linux

package window

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A WINDOW list is an array of complete 32-bit IDs. A server declaring
// trailing bytes that cannot contain a whole ID must not produce a successful,
// partial enumeration, including when its first reply declares Format 0.
func TestX11ClientListRejectsInconsistentRemainder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		format     byte
		bytesAfter uint32
		payload    []byte
	}{
		{"unaligned trailing bytes", 32, 2, []byte{50, 0, 0, 0}},
		{"unset format but trailing data", 0, 4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if p != b.atomNetClientList {
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
				}
				calls++
				if calls > 1 {
					t.Fatal("read another chunk despite invalid prior reply")
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: tc.payload, BytesAfter: tc.bytesAfter})
			}
			result, err := b.List(context.Background())
			if err == nil || len(result) != 0 || !strings.Contains(err.Error(), "_NET_CLIENT_LIST") {
				t.Fatalf("invalid list was accepted: result=%v err=%v", result, err)
			}
		})
	}
}
