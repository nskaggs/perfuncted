//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A process ID must be a positive signed Linux PID encoded as exactly one
// 32-bit EWMH CARDINAL. Non-32-bit or out-of-range properties cannot be
// promoted into the public window identity metadata.
func TestX11WindowPIDRejectsMalformedOrOutOfRangeProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		data   []byte
	}{
		{"8 bit format", 8, []byte{17, 0, 0, 0}},
		{"16 bit format", 16, []byte{17, 0, 0, 0}},
		{"zero pid", 32, []byte{0, 0, 0, 0}},
		{"negative signed pid", 32, []byte{0xff, 0xff, 0xff, 0xff}},
		{"sign bit set", 32, []byte{0, 0, 0, 0x80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetWMPID || kind != xproto.AtomCardinal {
					t.Fatalf("wrong PID lookup: property=%d kind=%d", property, kind)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Type: xproto.AtomCardinal, Format: tc.format, Value: tc.data})
			}
			if got := b.windowPID(50); got != 0 {
				t.Fatalf("malformed PID property became %d", got)
			}
		})
	}
}
