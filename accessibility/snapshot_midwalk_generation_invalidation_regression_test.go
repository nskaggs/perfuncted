package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// The registry may advance its handle epoch while a desktop snapshot is
// traversing a provider. Explicit handles must fail closed, while a caller
// requesting the current desktop may retry at most once against a new epoch.
func TestSnapshotMidWalkEpochInvalidationRejectsOldRootAndCache(t *testing.T) {
	for _, tc := range []struct {
		name          string
		invalidations int
		explicitRoot  bool
		wantReads     int
		wantError     bool
	}{
		{name: "desktop retries one invalidation", invalidations: 1, wantReads: 2},
		{name: "desktop bounds repeated invalidation", invalidations: 2, wantReads: 2, wantError: true},
		{name: "explicit desktop handle fails closed", invalidations: 1, explicitRoot: true, wantReads: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend *dbusBackend
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
				desktop.childrenHook = func() {
					if current := live.Load(); current != nil {
						if reads.Add(1) <= int32(tc.invalidations) {
							current.Invalidate(NodeID{})
						}
					}
				}
			})
			live.Store(backend)
			root := NodeID{}
			if tc.explicitRoot {
				root = backend.desktop()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			snap, err := backend.Snapshot(ctx, root, SnapshotOptions{Fresh: true, AllowDesktopRoot: true})
			if tc.wantError {
				if !errors.Is(err, ErrStaleGeneration) && !errors.Is(err, ErrStaleNode) {
					t.Fatalf("epoch rollover exposed stale snapshot: err=%v generation=%d", err, snap.Generation)
				}
				if len(snap.Nodes) != 0 {
					t.Fatalf("stale snapshot exposed %d nodes", len(snap.Nodes))
				}
				backend.mu.RLock()
				cacheCount := len(backend.cache)
				backend.mu.RUnlock()
				if cacheCount != 0 {
					t.Fatalf("stale snapshot published %d cache entries", cacheCount)
				}
			} else {
				if err != nil {
					t.Fatalf("desktop retry: %v", err)
				}
				if snap.Generation != backend.Generation() || len(snap.Nodes) == 0 {
					t.Fatalf("retry returned old or empty generation: result=%d current=%d nodes=%d", snap.Generation, backend.Generation(), len(snap.Nodes))
				}
			}
			if reads.Load() != int32(tc.wantReads) {
				t.Fatalf("provider reads=%d want=%d", reads.Load(), tc.wantReads)
			}
		})
	}
}
