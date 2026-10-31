package window

import (
	"testing"
)

// Window titles and app IDs originate with untrusted clients. Reject embedded
// NUL and invalid UTF-8 rather than allowing spoofed or ambiguous metadata.
func TestWaylandForeignToplevelRejectsMalformedStringsWithoutLosingValidMetadata(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"embedded NUL", encodeWaylandTestString("Trusted\x00Forged")},
		{"invalid UTF8", []byte{3, 0, 0, 0, 0xff, 0xfe, 0, 0}},
		{"missing terminator", []byte{3, 0, 0, 0, 'b', 'a', 'd', 0xff}},
		{"short packet", []byte{1, 2}},
	}
	for _, proto := range []struct {
		name                   string
		ext                    bool
		titleOpcode, appOpcode uint32
	}{{"wlr", false, 0, 1}, {"ext", true, 2, 3}} {
		for _, tc := range cases {
			t.Run(proto.name+"/"+tc.name, func(t *testing.T) {
				m := &WaylandWindowManager{toplevels: map[uint32]*Info{}}
				info := &Info{Title: "Existing title", AppID: "existing.app"}
				m.toplevels[77] = info
				deliver := func(op uint32) {
					if proto.ext {
						m.handleExtToplevelEvent(77, info, op, tc.payload)
					} else {
						m.handleWLRToplevelEvent(77, info, op, tc.payload)
					}
				}
				deliver(proto.titleOpcode)
				deliver(proto.appOpcode)
				if info.Title != "Existing title" || info.AppID != "existing.app" {
					t.Fatalf("invalid metadata changed to title=%q app=%q", info.Title, info.AppID)
				}
			})
		}
	}
}

func TestWaylandForeignToplevelAcceptsEmptyStringAndUnicodeNames(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", ""}, {"Café 日本", "Café 日本"}} {
		payload := encodeWaylandTestString(tc.input)
		value, valid := parseWaylandString(payload)
		if !valid || value != tc.want {
			t.Errorf("parse valid title %q: %q valid=%v", tc.input, value, valid)
		}
	}
}
