package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// Application enumeration must discard a child list observed across a
// generation change, retry once against the current desktop, and bound repeats.
func TestApplicationsRetriesOneStaleDesktopGenerationAndBoundsRepeat(t *testing.T) {
	for _, tc := range []struct {
		name             string
		invalidations    int32
		wantGeneration   uint64
		wantApplications int
		wantStaleError   bool
	}{
		{name: "one generation change retries", invalidations: 1, wantGeneration: 2, wantApplications: 1},
		{name: "repeated changes exhaust retry", invalidations: 2, wantGeneration: 3, wantStaleError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var active atomic.Bool
			var reads atomic.Int32
			var invalidations atomic.Int32
			invalidations.Store(tc.invalidations)
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					if !active.Load() {
						return
					}
					if reads.Add(1) <= invalidations.Load() {
						if backend := live.Load(); backend != nil {
							backend.Invalidate(NodeID{})
						}
					}
				}
			})
			live.Store(backend)
			active.Store(true)

			applications, err := backend.Applications(context.Background())
			if tc.wantStaleError {
				if !errors.Is(err, ErrStaleGeneration) || len(applications) != 0 {
					t.Fatalf("repeated generation changes returned apps=%+v err=%v, want bounded stale failure", applications, err)
				}
			} else if err != nil || len(applications) != tc.wantApplications || applications[0].ID.Generation != tc.wantGeneration {
				t.Fatalf("retried enumeration apps=%+v err=%v, want generation %d", applications, err, tc.wantGeneration)
			}
			if reads.Load() != 2 || backend.Generation() != tc.wantGeneration {
				t.Fatalf("enumeration reads=%d generation=%d, want two reads and generation %d", reads.Load(), backend.Generation(), tc.wantGeneration)
			}
		})
	}
}
