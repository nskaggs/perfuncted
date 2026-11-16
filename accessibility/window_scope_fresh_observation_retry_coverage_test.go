package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// A fresh window correlation must not publish identity gathered across a
// desktop observation-history rollover. One rollover permits a new lookup;
// repeated rollovers must exhaust the two-attempt retry budget.
func TestFreshWindowScopeBoundsObservationHistoryRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rollovers int
		wantError bool
	}{
		{name: "single rollover retries", rollovers: 1},
		{name: "repeated rollovers fail closed", rollovers: 2, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					count := reads.Add(1)
					if b := live.Load(); b != nil && count <= int32(tc.rollovers) {
						b.mu.Lock()
						b.observationRevision++
						b.observationFloor = b.observationRevision
						b.mu.Unlock()
					}
				}
			})
			live.Store(backend)
			scope, err := backend.ResolveWindowFresh(context.Background(), target)
			if tc.wantError {
				if !errors.Is(err, ErrObservationChanged) || scope.Root.valid() {
					t.Fatalf("incomplete observation history exposed scope=%+v err=%v", scope, err)
				}
			} else if err != nil || !scope.Root.valid() || scope.Generation != backend.Generation() {
				t.Fatalf("fresh retry failed: scope=%+v err=%v", scope, err)
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("desktop reads=%d; want bounded two-attempt lookup", got)
			}
		})
	}
}
