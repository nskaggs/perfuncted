package accessibility

import (
	"context"
	"testing"
)

// A mismatched application must not be queried for window children: besides
// wasting a provider round-trip it can expose another application's private
// accessibility tree to the window correlation request.
func TestFreshWindowCorrelationSkipsUnrelatedApplicationWithoutBusAccess(t *testing.T) {
	const bus = "org.example.Unrelated"
	b := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: bus, ObjectPath: "/app", Generation: 9, Incarnation: 1}}, PID: 6001}
	target := WindowTarget{ID: "selected-window", PID: 6002}
	candidates, observed, err := b.windowCandidatesFresh(context.Background(), target, []Application{app})
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("unrelated provider was observed: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// The non-fresh path must enforce the same PID ownership filter before
	// attempting to use any cached or live provider child identities.
	if got := b.windowCandidates(context.Background(), target, []Application{app}); len(got) != 0 {
		t.Fatalf("unrelated cached app was inspected: %+v", got)
	}
}

// A stale app may not establish scope even if its PID matches: generation
// checks precede every provider or cache lookup in fresh correlation.
func TestFreshWindowCorrelationRejectsRetiredApplicationBeforeProbing(t *testing.T) {
	b := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8, Incarnation: 1}}, PID: 6002}
	target := WindowTarget{ID: "selected-window", PID: 6002}
	candidates, observed, err := b.windowCandidatesFresh(context.Background(), target, []Application{app})
	if err != ErrStaleGeneration || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("retired app authorized fresh lookup: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
}
