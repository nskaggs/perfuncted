package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A toolkit label is not trustworthy unless the node resolves to a live
// application and that application's ToolkitName property can be read.
// Errors must leave the per-bus toolkit cache empty instead of retaining an
// invented or partial identity that future calls could reuse.
func TestPrivateATSPIToolkitResolutionRejectsMissingOrUnownedApplications(t *testing.T) {
	for _, tc := range []struct {
		name        string
		application objectRef
		want        string
	}{
		{"missing application reference", objectRef{BusName: registryName, ObjectPath: nullObjectPath}, "node has no application"},
		{"application service unavailable", objectRef{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/missing")}, ""},
		{"application property object missing", objectRef{BusName: registryName, ObjectPath: dbus.ObjectPath("/missing")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) { frame.application = tc.application })
			name, err := b.toolkitForNode(context.Background(), id)
			if err == nil || name != "" || (tc.want != "" && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("unverified toolkit accepted: name=%q err=%v", name, err)
			}
			b.mu.RLock()
			cached := len(b.toolkits)
			b.mu.RUnlock()
			if cached != 0 {
				t.Fatalf("unverified toolkit entered cache: %d entries", cached)
			}
			if tc.want != "" && errors.Is(err, ErrStaleNode) {
				t.Fatalf("missing application reported as stale handle: %v", err)
			}
		})
	}
}
