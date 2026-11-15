package accessibility

import (
	"context"
	"strings"
	"testing"
)

// Fresh window correlation must refuse incomplete provider identity, even
// when the window's name and process ownership were otherwise plausible.
// Exercise independently failed properties and methods over private AT-SPI.
func TestFreshWindowCandidatesRejectPartialProviderIdentity(t *testing.T) {
	for _, tc := range []struct {
		name            string
		fail            func(*syntheticATSPINode)
		needDescription bool
	}{
		{"description unavailable", func(n *syntheticATSPINode) { n.propertyFailure = "Description" }, true},
		{"role unavailable", func(n *syntheticATSPINode) { n.roleFailure = "role disconnected" }, true},
		{"states unavailable", func(n *syntheticATSPINode) { n.stateFailure = "states disconnected" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) { tc.fail(frame) })
			parent := NodeID{BusName: id.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: id.Generation}
			got, err := backend.readWindowCandidateFresh(context.Background(), id, parent, tc.needDescription)
			if err == nil || got.ID.valid() || got.Name != "" || got.Role != "" {
				t.Fatalf("partial provider identity admitted: node=%+v err=%v", got, err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "provider refused") && !strings.Contains(err.Error(), "disconnected") {
				t.Fatalf("provider failure not preserved: %v", err)
			}
		})
	}
}
