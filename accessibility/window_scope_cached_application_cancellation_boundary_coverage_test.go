package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Cached window application discovery still requires a live caller at each
// application boundary. Cancellation must not return a partial owner list.
func TestWindowApplicationsCachedCancellationBoundaryMatrix(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = append(root.children, root.children[0])
	})
	base, stop := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	apps, err := backend.windowApplications(baseline, target)
	stop()
	if err != nil || len(apps) != 2 {
		t.Fatalf("baseline cached owners=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	if checks < 2 {
		t.Fatalf("too few cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		live, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: live, cancel: cancel, threshold: index}
		result, readErr := backend.windowApplications(ctx, target)
		canceledDuringRead := live.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(result) != 0) {
			t.Fatalf("cancel on boundary %d/%d returned owners=%+v err=%v", index, checks, result, readErr)
		}
	}
}
