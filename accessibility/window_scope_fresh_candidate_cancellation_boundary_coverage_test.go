package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Cancel every observed boundary in fresh candidate enumeration against an
// isolated AT-SPI provider. No partial candidate set or observed private node
// may be released once the caller's scope authority has been revoked.
func TestFreshWindowCandidateEnumerationCancellationBoundaryMatrix(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackend(t, false)
	app := Application{Node: Node{ID: frameID}, PID: 7391}
	target := WindowTarget{ID: "managed-7391", PID: 7391}
	base, stop := context.WithCancel(context.Background())
	probe := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	candidates, observed, err := backend.windowCandidatesFresh(probe, target, []Application{app})
	stop()
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("baseline fresh candidate enumeration: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
	checks := probe.calls.Load()
	if checks < 3 {
		t.Fatalf("insufficient candidate cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		live, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: live, cancel: cancel, threshold: index}
		got, seen, readErr := backend.windowCandidatesFresh(ctx, target, []Application{app})
		canceledDuringRead := live.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0 || len(seen) != 0) {
			t.Fatalf("cancel at check %d/%d published candidates=%+v observed=%+v err=%v", index, checks, got, seen, readErr)
		}
	}
}
