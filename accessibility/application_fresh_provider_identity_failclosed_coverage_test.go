package accessibility

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A matching application cannot become an authorized fresh scope if the
// provider withdraws its name or window-title identity while it is read.
// Use an isolated AT-SPI provider with a directly exposed application node.
func TestFreshApplicationLookupRejectsUnavailableProviderIdentity(t *testing.T) {
	for _, key := range []string{"Name", "Description"} {
		t.Run(key, func(t *testing.T) {
			b, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
				frame.propertyFailure = key
			})
			filter := ApplicationFilter{Name: "Editor", WindowTitle: "document"}
			app, err := b.FindApplicationFresh(context.Background(), filter)
			if err == nil || app.ID.valid() || !strings.Contains(err.Error(), "read application ") {
				t.Fatalf("missing %s authorized an application: app=%+v err=%v", key, app, err)
			}
		})
	}
}
