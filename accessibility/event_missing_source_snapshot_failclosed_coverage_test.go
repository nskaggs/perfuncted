package accessibility

import (
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// A provider event that cannot identify its source must not authorize a
// snapshot captured before that event, even if its visible payload appears
// benign. Fail closed before the result can enter the reusable cache.
func TestUnknownEventSourcePreventsStaleSnapshotPublication(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	snapshot := Snapshot{Root: Node{ID: root, Role: "window"}, Nodes: []Node{{ID: root, Role: "window"}}, Generation: 7, CapturedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
		event  Event
	}{
		{"missing signal", nil, Event{Kind: "object:property-change", Node: root}},
		{"missing sender", &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Path: "/window"}, Event{Kind: "object:property-change"}},
		{"malformed cache addition", &dbus.Signal{Name: "org.a11y.atspi.Cache:AddAccessible", Sender: "org.synthetic.Editor", Body: []any{"invalid cache item"}}, Event{Kind: "cache:add"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, observationRevision: 1}
			b.recordObservationLocked(tc.signal, tc.event, 2)
			b.observationRevision = 2
			if len(b.observationHistory) != 1 || !b.observationHistory[0].unknown || b.observationHistory[0].revision != 2 {
				t.Fatalf("unidentified provider event was not recorded as an unknown observation: %+v", b.observationHistory)
			}
			if err := b.observationChangesError(1, root, snapshot, true); !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("unidentified event did not invalidate in-flight observation: %v", err)
			}
			if err := b.publishSnapshot("stale", 7, 1, root, true, snapshot); !errors.Is(err, ErrObservationChanged) || len(b.cache) != 0 {
				t.Fatalf("stale snapshot became reusable: %v cache=%+v", err, b.cache)
			}
		})
	}
}
