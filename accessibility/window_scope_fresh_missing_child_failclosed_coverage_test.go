package accessibility

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Applications can publish child references to objects that disappear before
// the compositor resolves their identity. Fresh correlation cannot silently
// discard these failures and publish an incomplete set of candidate windows.
func TestFreshWindowCandidateEnumerationRejectsVanishedChildObject(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished_child")}}
	})
	app := Application{Node: Node{ID: frameID}, PID: 4317}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-4317", PID: 4317}, []Application{app})
	if err == nil || len(candidates) != 0 || len(observed) != 0 || !strings.Contains(err.Error(), "resolve fresh window candidate") {
		t.Fatalf("vanished child authorized incomplete candidates: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}
