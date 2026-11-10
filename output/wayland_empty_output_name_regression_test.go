package output

import (
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

// Output names are user-facing stable identifiers; an empty compositor name
// must not erase the unique registry-based fallback or a previous valid name.
func TestWaylandEmptyOutputNamesPreserveStableIdentity(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 23, Interface: "wl_output", Version: 4})
	fallback := out.info.Name
	if fallback == "" {
		t.Fatal("missing generated output identity")
	}
	protocol := &wl.RawProxy{}
	out.updateProxy(protocol)
	protocol.OnEvent(4, -1, wlStringData(""))
	if out.info.Name != fallback {
		t.Fatalf("empty wl_output name overwrote fallback: %q", out.info.Name)
	}
	protocol.OnEvent(4, -1, wlStringData("DP-1"))
	if out.info.Name != "DP-1" {
		t.Fatalf("valid output name lost: %q", out.info.Name)
	}
	protocol.OnEvent(4, -1, wlStringData(""))
	if out.info.Name != "DP-1" {
		t.Fatalf("empty wl_output name erased valid name: %q", out.info.Name)
	}
	xdg := &wl.RawProxy{}
	out.updateXDGProxy(xdg)
	xdg.OnEvent(3, -1, wlStringData(""))
	if out.info.Name != "DP-1" {
		t.Fatalf("empty xdg_output name erased valid name: %q", out.info.Name)
	}
	xdg.OnEvent(3, -1, wlStringData("HDMI-A-1"))
	if out.info.Name != "HDMI-A-1" {
		t.Fatalf("valid xdg name was not applied: %q", out.info.Name)
	}
}
