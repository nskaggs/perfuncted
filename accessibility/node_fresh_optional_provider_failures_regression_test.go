package accessibility

import (
	"context"
	"strings"
	"testing"
)

// Failed optional AT-SPI fields must be explained to a fresh snapshot caller
// rather than silently accepted as complete evidence. Cached best-effort
// reads must not claim those warnings as fresh provider observations.
func TestFreshNodeReadReportsTextAndAttributeProviderFailures(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.propertyFailure = "CharacterCount"
		frame.attributesFailure = "synthetic attributes unavailable"
	})
	fresh, err := backend.readNodeFresh(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatalf("fresh read failed instead of reporting optional property warnings: %v", err)
	}
	if fresh.Name != "Editor — document" || fresh.Role != "frame" || fresh.Text != "" || len(fresh.Attributes) != 0 {
		t.Fatalf("fresh partial read misrepresented the node: %+v", fresh)
	}
	warnings := strings.Join(fresh.Warnings, "\n")
	for _, wanted := range []string{"CharacterCount", "GetAttributes", "synthetic attributes unavailable"} {
		if !strings.Contains(warnings, wanted) {
			t.Fatalf("fresh read omitted %q warning: %v", wanted, fresh.Warnings)
		}
	}
	cached, err := backend.readNode(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cached.Warnings, "\n"), "CharacterCount") || strings.Contains(strings.Join(cached.Warnings, "\n"), "GetAttributes") {
		t.Fatalf("best-effort cached read was mislabeled as fresh provider evidence: %v", cached.Warnings)
	}
}
