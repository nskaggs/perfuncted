package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// An event-history rollover while the provider is enumerating applications
// makes a successful identity read unverifiable, even if the handle generation
// did not change. Retry once; never publish from two incomplete observations.
func TestFreshApplicationLookupRejectsEvictedObservationHistory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate int
		wantError  bool
	}{
		{name: "single observation rollover retries", invalidate: 1},
		{name: "repeated observation rollover fails closed", invalidate: 2, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			b, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					count := reads.Add(1)
					if backend := live.Load(); backend != nil && count <= int32(tc.invalidate) {
						backend.mu.Lock()
						backend.observationRevision++
						backend.observationFloor = backend.observationRevision
						backend.mu.Unlock()
					}
				}
			})
			live.Store(b)
			app, err := b.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
			if tc.wantError {
				if !errors.Is(err, ErrObservationChanged) || app.ID.valid() {
					t.Fatalf("incomplete history published app=%+v err=%v", app, err)
				}
			} else if err != nil || app.Name != "Editor" || app.ID.Generation != b.Generation() {
				t.Fatalf("valid retry failed: app=%+v err=%v", app, err)
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("provider reads=%d, want two bounded reads", got)
			}
		})
	}
}
