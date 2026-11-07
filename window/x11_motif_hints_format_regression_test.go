//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Motif hints are exactly five 32-bit values. An incorrectly formatted reply
// must not authorize stripping decoration from the window geometry.
func TestX11MotifHintsRejectMalformedPropertyFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
	}{
		{"8-bit", 8}, {"16-bit", 16}, {"unset", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomMotifWMHints || typ != b.atomMotifWMHints {
					t.Fatalf("unexpected hint property %d type %d", property, typ)
				}
				data := make([]byte, 20)
				data[0] = 2 // decorations flag set
				// decorations value 0 means no decorations, but only in valid 32-bit hints.
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: data})
			}
			if !b.windowHasDecoration(50) {
				t.Fatal("malformed Motif hints hid decorations")
			}
		})
	}
}
