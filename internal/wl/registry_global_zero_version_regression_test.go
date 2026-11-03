package wl

import "testing"

// Registry interface versions begin at one. A complete advertisement with
// version zero must not be published as a bindable interface.
func TestRegistryRejectsZeroVersionGlobalAndRetainsValidAdvertisement(t *testing.T) {
	var got []GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(event GlobalEvent) { got = append(got, event) })
	r.Dispatch(0, -1, registryGlobalData(41, "wl_output", 0))
	if len(got) != 0 {
		t.Fatalf("invalid version-zero global was advertised: %+v", got)
	}
	r.Dispatch(0, -1, registryGlobalData(42, "wl_output", 1))
	if len(got) != 1 || got[0] != (GlobalEvent{Name: 42, Interface: "wl_output", Version: 1}) {
		t.Fatalf("valid minimum-version global not published exactly once: %+v", got)
	}
}
