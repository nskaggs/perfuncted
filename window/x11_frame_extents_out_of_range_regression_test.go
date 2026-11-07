//go:build linux

package window

import (
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// EWMH frame extents describe a small decoration around a window. An
// untrusted 32-bit CARDINAL such as 0xffffffff must not expand the returned
// geometry into billions of pixels or negative coordinates.
func TestX11FrameExtentsRejectOutOfRangeDecorationValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extents [4]uint32
	}{
		{"huge left", [4]uint32{^uint32(0), 0, 0, 0}},
		{"huge right", [4]uint32{0, ^uint32(0), 0, 0}},
		{"huge top", [4]uint32{0, 0, ^uint32(0), 0}},
		{"huge bottom", [4]uint32{0, 0, 0, ^uint32(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, true, "Test")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if p == b.atomNetFrameExtent {
					buf := make([]byte, 16)
					for i, v := range tc.extents {
						binary.LittleEndian.PutUint32(buf[i*4:], v)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: buf})
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
			}
			x, y, w, h := b.windowGeometry(50)
			if x != 10 || y != 20 || w != 800 || h != 600 {
				t.Fatalf("untrusted frame extents changed geometry to (%d,%d,%d,%d)", x, y, w, h)
			}
		})
	}
}
