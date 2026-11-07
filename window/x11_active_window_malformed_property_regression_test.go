//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// The root _NET_ACTIVE_WINDOW property is exactly one 32-bit WINDOW. A
// malformed property cannot claim focus for an arbitrary window ID.
func TestX11ActiveWindowRejectsMalformedProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		value  []byte
	}{
		{"8 bit property", 8, []byte{50, 0, 0, 0}},
		{"16 bit property", 16, []byte{50, 0, 0, 0}},
		{"extra window ID", 32, []byte{50, 0, 0, 0, 51, 0, 0, 0}},
		{"short property", 32, []byte{50, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetActiveWindow || kind != xproto.AtomWindow {
					t.Fatalf("unexpected property/type %d/%d", property, kind)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: tc.value})
			}
			win, err := b.activeWindow()
			if err != nil || win != 0 {
				t.Fatalf("malformed active-window property reported window=%d err=%v", win, err)
			}
		})
	}
}
