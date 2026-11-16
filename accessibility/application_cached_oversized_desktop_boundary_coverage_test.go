package accessibility

import (
	"context"
	"testing"
)

// Best-effort application discovery must clamp its desktop work budget before
// contacting any provider. Repeated references beyond the limit must not
// cause unbounded application enumeration or produce more than the limit.
func TestApplicationsCapsOversizedPrivateATSPIDesktop(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		original := root.children[0]
		root.children = make([]objectRef, maxApplications+1)
		for i := range root.children {
			root.children[i] = original
		}
	})
	applications, err := backend.Applications(context.Background())
	if err != nil {
		t.Fatalf("bounded enumeration failed: %v", err)
	}
	if len(applications) > maxApplications {
		t.Fatalf("unbounded application enumeration: %d > %d", len(applications), maxApplications)
	}
}
