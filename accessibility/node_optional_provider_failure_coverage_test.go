package accessibility

import (
	"context"
	"testing"
)

// Optional interface and state reads must not erase an otherwise readable
// node identity when an AT-SPI provider rejects those protocol calls.
func TestReadNodePreservesIdentityWhenOptionalProviderReadsFail(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.interfacesFailure = "interfaces unavailable"
		frame.stateFailure = "states unavailable"
	})

	node, err := backend.readNode(context.Background(), id, NodeID{}, defaultMaxText, false)
	if err != nil {
		t.Fatalf("optional metadata failures rejected node identity: %v", err)
	}
	if node.ID != id || node.Name != "Editor — document" || node.Role != "frame" {
		t.Fatalf("provider failures lost core node identity: %+v", node)
	}
	if len(node.Interfaces) != 0 || len(node.States) != 0 || node.Enabled || node.Focused || node.Visible || node.Showing {
		t.Fatalf("failed optional reads published partial metadata: interfaces=%v states=%v node=%+v", node.Interfaces, node.States, node)
	}
}
