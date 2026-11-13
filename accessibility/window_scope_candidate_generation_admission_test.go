package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Application references recovered before a provider reconnect cannot be
// promoted into a fresh window scope after the backend generation changes.
// Even an otherwise matching PID must not authorize a stale application.
func TestFreshWindowCandidatesRejectOldApplicationGeneration(t *testing.T) {
	backend := &dbusBackend{generation: 8}
	stale := Application{Node: Node{ID: NodeID{BusName: "org.example.Editor", ObjectPath: "/application", Generation: 7}}, PID: 456}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-editor", PID: 456}, []Application{stale})
	if !errors.Is(err, ErrStaleGeneration) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("stale app authorized fresh window correlation: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// A caller's cancellation is authoritative even when no provider child
	// needs to be enumerated and no candidate can be returned.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	candidates, observed, err = backend.windowCandidatesFresh(ctx, WindowTarget{ID: "managed-editor"}, nil)
	if !errors.Is(err, context.Canceled) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("canceled correlation returned candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// No caller context must not implicitly authorize new provider reads.
	candidates, observed, err = backend.windowCandidatesFresh(nil, WindowTarget{ID: "managed-editor"}, nil)
	if err == nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("nil-context correlation accepted: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// Matching-generation empty results remain a valid observation, rather
	// than being mistaken for a stale provider or a canceled lookup.
	candidates, observed, err = backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-editor"}, nil)
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("empty current-generation correlation failed: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
}
