package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// After an authorized application has exposed its children, cancellation may
// race with the first child read. It must reject the entire candidate set,
// never publish a partial window identity.
func TestFreshWindowChildIterationCancellationBoundaryMatrix(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
	})
	app := Application{Node: Node{ID: id}, PID: 4754}
	target := WindowTarget{ID: "managed-4754", PID: 4754, Title: "Editor"}
	probeBase, probeCancel := context.WithCancel(context.Background())
	probe := &cancelAtAccessibilityContextRead{Context: probeBase, cancel: probeCancel, threshold: 10000}
	candidates, observed, err := backend.windowCandidatesFresh(probe, target, []Application{app})
	probeCancel()
	if err != nil || len(observed) != 1 || len(candidates) != 1 {
		t.Fatalf("baseline window candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
	checks := probe.calls.Load()
	if checks < 3 {
		t.Fatalf("not enough candidate admission checks: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
		got, seen, readErr := backend.windowCandidatesFresh(ctx, target, []Application{app})
		duringRead := base.Err() != nil
		cancel()
		if duringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0 || len(seen) != 0) {
			t.Fatalf("canceled at check %d/%d returned candidates=%+v observed=%+v err=%v", index, checks, got, seen, readErr)
		}
	}
}
