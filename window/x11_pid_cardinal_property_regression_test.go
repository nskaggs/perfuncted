//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// _NET_WM_PID has type CARDINAL under EWMH. XGetWindowProperty returns
// no value for a caller that requests a different property type.
func TestX11WindowPIDRequestsEWMHCardinalPropertyType(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	var requestedType xproto.Atom
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetWMPID {
			t.Fatalf("requested wrong property %d", property)
		}
		requestedType = typ
		if typ != xproto.AtomCardinal {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0, Type: xproto.AtomCardinal})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: []byte{0xd2, 0x04, 0, 0}})
	}
	if pid := b.windowPID(50); pid != 1234 {
		t.Fatalf("_NET_WM_PID returned %d, requested type=%d; want CARDINAL", pid, requestedType)
	}
}
