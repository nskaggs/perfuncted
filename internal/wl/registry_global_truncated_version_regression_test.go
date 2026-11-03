package wl

import (
	"testing"
)

// wl_registry.global has three mandatory arguments (name, interface, version).
// A compositor must not be able to advertise a usable capability using an
// incomplete event that omits the version field.
func TestRegistryRejectsTruncatedGlobalVersionBeforePublishing(t *testing.T) {
	var events []GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { events = append(events, ev) })
	complete := registryGlobalData(17, "wl_output", 4)
	if len(complete) < 4 {
		t.Fatal("invalid test fixture")
	}
	r.Dispatch(0, -1, complete[:len(complete)-4])
	if len(events) != 0 {
		t.Fatalf("truncated global advertised: %+v", events)
	}
	r.Dispatch(0, -1, complete)
	if len(events) != 1 || events[0] != (GlobalEvent{Name: 17, Interface: "wl_output", Version: 4}) {
		t.Fatalf("valid complete global was lost: %+v", events)
	}
}
