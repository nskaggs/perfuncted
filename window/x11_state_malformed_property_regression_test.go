//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A malformed _NET_WM_STATE property must not claim fullscreen, minimized,
// or maximized state from a partial or incorrectly typed atom list.
func TestX11WindowStateRejectsMalformedProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		value  []byte
	}{
		{"8 bit state", 8, []byte{23, 0, 0, 0}},
		{"trailing partial atom", 32, []byte{23, 0, 0, 0, 99}},
		{"16 bit state", 16, []byte{23, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetWMState || kind != xproto.AtomAtom {
					t.Fatalf("unexpected property/type %d/%d", property, kind)
				}
				v := append([]byte(nil), tc.value...)
				v[0] = byte(b.atomNetWMStateFullscreen)
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: v})
			}
			_, _, fullscreen := b.windowState(50)
			if fullscreen {
				t.Fatalf("malformed _NET_WM_STATE returned fullscreen=true")
			}
		})
	}
}
