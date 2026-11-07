//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// _NET_FRAME_EXTENTS uses CARDINAL, not the property atom as its value type.
// A compliant X server returns no value for a mismatched GetProperty type,
// which otherwise drops the decoration offsets from window geometry.
func TestX11WindowGeometryRequestsCardinalFrameExtents(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Test")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		switch property {
		case b.atomMotifWMHints:
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
		case b.atomNetFrameExtent:
			if kind != xproto.AtomCardinal {
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0, Type: xproto.AtomCardinal})
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: []byte{5, 0, 0, 0, 7, 0, 0, 0, 11, 0, 0, 0, 13, 0, 0, 0}})
		default:
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
		}
	}
	x, y, w, h := b.windowGeometry(50)
	if x != 5 || y != 9 || w != 812 || h != 624 {
		t.Fatalf("decoration extents absent: geometry=(%d,%d,%d,%d) want (5,9,812,624)", x, y, w, h)
	}
}
