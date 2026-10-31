package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Wayland strings have one terminal NUL, not an interior NUL that can hide
// suffixes from C clients or poison display-name comparison/logging.
func TestWaylandOutputRejectsEmbeddedNULInAdvertisedNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xdg    bool
		opcode uint32
	}{
		{"wl_output.name", false, 4},
		{"wl_output.description", false, 5},
		{"xdg_output.name", true, 3},
		{"xdg_output.description", true, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 6, Interface: "wl_output", Version: 4})
			out.info.Name = "Original-Display"
			out.info.Description = "Original-Description"
			proxy := &wl.RawProxy{}
			if tc.xdg {
				out.updateXDGProxy(proxy)
			} else {
				out.updateProxy(proxy)
			}
			malformed := wlStringData("Trusted\x00Forged")
			if got, _, ok := readWlString(malformed, 0); ok {
				t.Fatalf("embedded-NUL wire string accepted as %q", got)
			}
			proxy.OnEvent(tc.opcode, 0, malformed)
			if out.info.Name != "Original-Display" || out.info.Description != "Original-Description" {
				t.Fatalf("untrusted name changed output metadata: %+v", out.info)
			}
		})
	}
}

// Compositor strings are UTF-8. Invalid byte sequences must not be exposed in
// output identifiers consumed by logs, JSON, or UI accessibility matching.
func TestWaylandOutputRejectsInvalidUTF8NameWithoutLosingLastGoodValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xdg    bool
		opcode uint32
	}{
		{"wl_output.name", false, 4},
		{"xdg_output.name", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 21, Interface: "wl_output", Version: 4})
			p := &wl.RawProxy{}
			if tc.xdg {
				out.updateXDGProxy(p)
			} else {
				out.updateProxy(p)
			}
			good := wlStringData("Display-é-日本")
			p.OnEvent(tc.opcode, 0, good)
			if out.info.Name != "Display-é-日本" {
				t.Fatalf("valid UTF-8 output name rejected: %q", out.info.Name)
			}
			bad := []byte{0xff, 0xfe, 0}
			wire := make([]byte, 4+4)
			wl.PutUint32(wire[:4], uint32(len(bad)))
			copy(wire[4:], bad)
			if _, _, ok := readWlString(wire, 0); ok {
				t.Fatal("malformed UTF-8 accepted")
			}
			p.OnEvent(tc.opcode, 0, wire)
			if out.info.Name != "Display-é-日本" {
				t.Fatalf("invalid UTF-8 replaced last safe output name: %q", out.info.Name)
			}
		})
	}
}
