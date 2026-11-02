package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A caller may cancel after the desktop application inventory was read but
// before fresh candidate traversal begins. Even an empty candidate inventory
// must respect cancellation rather than masquerade as a successful read.
func TestFreshWindowCandidatesRefuseCanceledEmptyApplicationInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &dbusBackend{generation: 3}
	candidates, observed, err := backend.windowCandidatesFresh(ctx, WindowTarget{ID: "private", Title: "Console"}, nil)
	if !errors.Is(err, context.Canceled) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("canceled scope candidate traversal returned candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}
