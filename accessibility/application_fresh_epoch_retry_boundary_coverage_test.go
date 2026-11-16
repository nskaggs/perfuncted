package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// Fresh application identity must be consistent with one AT-SPI generation.
// When the desktop changes epochs during child enumeration, a single change
// may be retried, but repeated changes cannot release any application.
func TestFreshApplicationLookupBoundsDesktopEpochRetries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		invalidations int
		wantErr       bool
	}{
		{name: "one invalidation retries", invalidations: 1},
		{name: "repeated invalidation fails closed", invalidations: 2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					n := reads.Add(1)
					if b := live.Load(); b != nil && n <= int32(tc.invalidations) {
						b.Invalidate(NodeID{})
					}
				}
			})
			live.Store(backend)
			app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
			if tc.wantErr {
				if err == nil || app.ID.valid() || !errors.Is(err, ErrStaleGeneration) {
					t.Fatalf("repeated epoch changes exposed app=%+v err=%v", app, err)
				}
			} else if err != nil || app.Name != "Editor" || app.ID.Generation != backend.Generation() {
				t.Fatalf("retry did not return current application: app=%+v err=%v generation=%d", app, err, backend.Generation())
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("provider reads=%d, want 2 bounded attempts", got)
			}
		})
	}
}
