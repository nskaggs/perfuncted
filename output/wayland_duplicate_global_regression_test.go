package output

import (
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

func TestWaylandOutputGlobalsDoNotDuplicateOneRegistryObject(t *testing.T) {
	globals := []wl.GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 9, Interface: "wl_seat", Version: 7},
		{Name: 4, Interface: "wl_output", Version: 3},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_output", Version: 4},
	}
	got := outputGlobals(globals)
	if len(got) != 2 || got[0].Name != 4 || got[1].Name != 12 {
		t.Fatalf("a repeated advertisement bound more than one proxy per global: %+v", got)
	}
}

func TestWaylandTopologyChangesDoNotReaddDuplicateGlobalIDs(t *testing.T) {
	l := &WaylandLister{}
	globals := []wl.GlobalEvent{{Name: 4, Interface: "wl_output", Version: 4}, {Name: 4, Interface: "wl_output", Version: 4}}
	desired, added, removed := l.topologyChanges(globals)
	if len(desired) != 1 || len(added) != 1 || len(removed) != 0 {
		t.Fatalf("duplicate global causes duplicate physical output: desired=%d added=%d removed=%d", len(desired), len(added), len(removed))
	}
}
