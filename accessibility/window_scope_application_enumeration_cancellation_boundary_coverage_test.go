package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Fresh desktop application enumeration is an authorization precondition for
// window correlation. Cancellation at any observed boundary must never leak a
// partially enumerated set of application identities.
func TestWindowApplicationsFreshCancellationBoundaryMatrix(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	base, stop := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	apps, err := backend.windowApplicationsFresh(baseline, target)
	stop()
	if err != nil || len(apps) != 1 {
		t.Fatalf("baseline applications=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	if checks < 2 {
		t.Fatalf("too few cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		ctxBase, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: ctxBase, cancel: cancel, threshold: index}
		got, readErr := backend.windowApplicationsFresh(ctx, target)
		canceledDuringRead := ctxBase.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0) {
			t.Fatalf("cancellation on check %d/%d released applications=%+v err=%v", index, checks, got, readErr)
		}
	}
}
