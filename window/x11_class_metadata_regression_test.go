//go:build linux

package window

import (
	"testing"
	"unicode/utf8"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// ICCCM WM_CLASS contains two NUL-terminated STRING (Latin-1) fields.
// Incorrect wire format or unterminated/extra fields cannot be trusted as
// the window's instance and resource class.
func TestX11WindowClassValidatesICCCMWireEncoding(t *testing.T) {
	for _, tc := range []struct {
		name            string
		format          byte
		payload         []byte
		instance, class string
	}{
		{"latin1 class", 8, []byte{'C', 'a', 'f', 0xe9, 0, 'E', 'd', 'i', 't', 0xe9, 0}, "Café", "Edité"},
		{"invalid 32 bit format", 32, []byte("inst\x00Class\x00"), "", ""},
		{"missing final terminator", 8, []byte("inst\x00Class"), "", ""},
		{"missing separator", 8, []byte("instClass"), "", ""},
		{"extra hidden field", 8, []byte("inst\x00Class\x00Spoofed\x00"), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != xproto.AtomWmClass || typ != xproto.AtomString {
					t.Fatalf("wrong WM_CLASS query: %d %d", property, typ)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Type: xproto.AtomString, Value: tc.payload})
			}
			instance, class := b.windowClass(50)
			if instance != tc.instance || class != tc.class || !utf8.ValidString(instance) || !utf8.ValidString(class) {
				t.Fatalf("WM_CLASS=(%q,%q) want (%q,%q)", instance, class, tc.instance, tc.class)
			}
		})
	}
}
