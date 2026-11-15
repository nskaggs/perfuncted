package accessibility

import (
	"context"
	"testing"
)

// A candidate object can vanish after its application has been enumerated.
// A fresh window resolution must reject missing provider properties rather
// than promoting the application's PID or a partial candidate to authority.
func TestWindowCandidateFreshRejectsVanishedObjectProperties(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackend(t, false)
	parent := NodeID{BusName: frameID.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: frameID.Generation}
	vanished := NodeID{BusName: frameID.BusName, ObjectPath: "/org/a11y/atspi/accessible/removed", Generation: frameID.Generation}
	for _, needDescription := range []bool{false, true} {
		got, err := backend.readWindowCandidateFresh(context.Background(), vanished, parent, needDescription)
		if err == nil || got.ID.valid() || got.Name != "" || got.Role != "" {
			t.Fatalf("removed AT-SPI object became a window: description=%v candidate=%+v err=%v", needDescription, got, err)
		}
	}
}
