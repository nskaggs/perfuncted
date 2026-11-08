//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// BytesAfter means GetProperty returned only a prefix. A partial title must
// never be returned as if it were the complete application-provided metadata.
func TestX11WindowTitleRejectsTruncatedProperties(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		modernAfter, legacyAfter uint32
		want                     string
	}{
		{"truncated modern uses complete legacy", 4, 0, "Complete legacy"},
		{"truncated modern and legacy are not published", 4, 4, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				switch p {
				case b.atomNetWMName:
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Prefix"), BytesAfter: tc.modernAfter})
				case xproto.AtomWmName:
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Complete legacy"), BytesAfter: tc.legacyAfter})
				default:
					t.Fatalf("unexpected title property %d", p)
					return nil
				}
			}
			if got := b.windowTitle(50); got != tc.want {
				t.Fatalf("truncated title got %q want %q", got, tc.want)
			}
		})
	}
}
