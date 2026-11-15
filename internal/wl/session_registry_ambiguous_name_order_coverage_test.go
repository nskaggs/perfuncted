package wl

import (
	"reflect"
	"testing"
)

// An untrusted compositor may advertise different interfaces or versions
// under the same numeric global name. Sort the observed snapshot with all
// tie-breakers so diagnostics and consumers do not depend on event arrival
// order, while retaining conflicting declarations for inspection.
func TestWaylandRegistryOrderingIsDeterministicForConflictingGlobalNames(t *testing.T) {
	observations := []GlobalEvent{
		{Name: 12, Interface: "wl_seat", Version: 3},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 4, Interface: "wl_output", Version: 1},
		{Name: 12, Interface: "wl_output", Version: 2},
		{Name: 12, Interface: "wp_viewporter", Version: 1},
	}
	want := []GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 1},
		{Name: 12, Interface: "wl_output", Version: 2},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_seat", Version: 3},
		{Name: 12, Interface: "wp_viewporter", Version: 1},
	}
	sortGlobals(observations)
	if !reflect.DeepEqual(observations, want) {
		t.Fatalf("conflicting compositor declarations sorted incorrectly: got=%+v want=%+v", observations, want)
	}
}
