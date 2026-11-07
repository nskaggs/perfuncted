//go:build linux

package window

import (
	"testing"
	"unicode/utf8"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A malformed application-owned _NET_WM_NAME must not become public metadata:
// invalid UTF-8 and embedded NUL can confuse consumers matching window titles.
// Legacy WM_NAME is ISO-8859-1, not UTF-8, so non-ASCII bytes must decode.
func TestX11WindowTitleRejectsMalformedUTF8AndDecodesLatin1Fallback(t *testing.T) {
	for _, tc := range []struct {
		name           string
		modern, legacy []byte
		want           string
	}{
		{"invalid utf8", []byte{0xff, 0xfe}, []byte("Fallback"), "Fallback"},
		{"embedded nul", []byte("Trusted\x00Forged"), []byte("Fallback"), "Fallback"},
		{"latin1 fallback", nil, []byte{'C', 'a', 'f', 0xe9}, "Café"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				switch property {
				case b.atomNetWMName:
					if kind != b.atomUTF8String {
						t.Fatalf("wrong modern title type %d", kind)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: tc.modern})
				case xproto.AtomWmName:
					if kind != xproto.AtomString {
						t.Fatalf("wrong legacy title type %d", kind)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: tc.legacy})
				default:
					t.Fatalf("unexpected property %d", property)
					return nil
				}
			}
			got := b.windowTitle(50)
			if got != tc.want || !utf8.ValidString(got) {
				t.Fatalf("title=%q (valid UTF8=%v) want %q", got, utf8.ValidString(got), tc.want)
			}
		})
	}
}
