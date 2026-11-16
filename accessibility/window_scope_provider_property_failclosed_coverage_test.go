package accessibility

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Provider-reported application identity is required before AppID-only
// matching. Neither a missing name nor a missing description is permission
// to invent a window owner. Cached candidate resolution must also discard
// a frame when its role cannot be read.
func TestWindowIdentityProviderPropertyFailuresCannotAuthorizeOwnership(t *testing.T) {
	for _, key := range []string{"Name", "Description"} {
		t.Run("application "+key, func(t *testing.T) {
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
				frame.propertyFailure = key
			})
			apps, err := backend.windowApplicationsFresh(context.Background(), WindowTarget{ID: "compositor-window", AppID: "Editor"})
			if err == nil || len(apps) != 0 || !strings.Contains(err.Error(), "read window application ") {
				t.Fatalf("missing provider %s admitted application identity: apps=%+v err=%v", key, apps, err)
			}
		})
	}
	t.Run("cached candidate role", func(t *testing.T) {
		backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) { frame.roleFailure = "role no longer available" })
		parent := NodeID{BusName: id.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: id.Generation}
		node, err := backend.readWindowCandidate(context.Background(), id, parent)
		if err == nil || node.ID.valid() || node.Role != "" {
			t.Fatalf("missing provider role admitted frame: node=%+v err=%v", node, err)
		}
	})
}
