package output

import (
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

// A malformed compositor announcement is not an authorized output. ID zero
// is the Wayland display object, and an interface version of zero cannot be
// bound; neither may appear in a screen enumeration or topology delta.
func TestWaylandOutputRegistryIgnoresInvalidGlobalIdentityAndVersion(t *testing.T) {
	advertised := []wl.GlobalEvent{
		{Name: 0, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_output", Version: 0},
		{Name: 13, Interface: "wl_output", Version: 4},
		{Name: 13, Interface: "wl_output", Version: 0},
		{Name: 14, Interface: "wl_seat", Version: 4},
	}
	filtered := outputGlobals(advertised)
	if len(filtered) != 1 || filtered[0].Name != 13 || filtered[0].Version != 4 {
		t.Fatalf("invalid registry outputs accepted: %+v", filtered)
	}
	l := &WaylandLister{}
	desired, added, removed := l.topologyChanges(filtered)
	if len(desired) != 1 || len(added) != 1 || len(removed) != 0 || added[0].globalID != 13 {
		t.Fatalf("invalid output topology accepted: desired=%v added=%v removed=%v", desired, added, removed)
	}
}
