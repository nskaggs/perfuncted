package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A matching application identity cannot establish window ownership when its
// provider is disconnected. Cached correlation skips the unreadable children;
// fresh correlation must fail closed without publishing a candidate scope.
func TestWindowCorrelationRejectsUnreadableMatchedApplicationChildren(t *testing.T) {
	backend := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.PrivateEditor", ObjectPath: "/app", Generation: 9}}, PID: 321}
	target := WindowTarget{ID: "managed-private-editor", PID: 321}
	if !windowAppMatches(app, target) {
		t.Fatal("test application did not match target ownership")
	}
	if got := backend.windowCandidates(context.Background(), target, []Application{app}); len(got) != 0 {
		t.Fatalf("disconnected provider yielded cached window candidates: %+v", got)
	}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app})
	if !errors.Is(err, ErrDisconnected) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("disconnected provider authorized fresh window: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}
