package accessibility

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestDesktopSnapshotRetriesObservedChangesAndRefusesRepeatedChange(t *testing.T) {
	for _, tc := range []struct {
		name         string
		changeCount  int32
		wantErr      error
		wantRootName string
	}{
		{name: "retries one changed observation", changeCount: 1, wantRootName: "Desktop after change 1"},
		{name: "exhausts after repeated changes", changeCount: 2, wantErr: ErrStaleGeneration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend *dbusBackend
			var reads atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop, _ *syntheticATSPINode) {
				desktop.childrenHook = func() {
					read := reads.Add(1)
					if read > tc.changeCount {
						return
					}
					if read == 1 {
						close(entered)
						<-release
					}
					desktop.name = fmt.Sprintf("Desktop after change %d", read)
					signal := &dbus.Signal{
						Name:   "org.a11y.atspi.Event.Object.PropertyChange",
						Sender: registryName,
						Path:   desktopPath,
						Body:   []any{"Name", desktop.name},
					}
					backend.prepareEventTransition(signal, signalEvent(signal))
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan struct {
				snapshot Snapshot
				err      error
			}, 1)
			go func() {
				result <- func() (out struct {
					snapshot Snapshot
					err      error
				}) {
					out.snapshot, out.err = backend.Snapshot(ctx, NodeID{}, SnapshotOptions{Fresh: true, AllowDesktopRoot: true})
					return out
				}()
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				close(release)
				t.Fatalf("desktop traversal did not reach its provider boundary: %v", ctx.Err())
			}
			close(release)
			select {
			case got := <-result:
				if tc.wantErr != nil {
					if !errors.Is(got.err, tc.wantErr) {
						t.Fatalf("desktop snapshot error = %v, want %v", got.err, tc.wantErr)
					}
					backend.mu.RLock()
					cacheEntries := len(backend.cache)
					backend.mu.RUnlock()
					if cacheEntries != 0 {
						t.Fatalf("unstable desktop snapshot published cache entries: %d", cacheEntries)
					}
				} else {
					if got.err != nil || got.snapshot.Root.Name != tc.wantRootName {
						t.Fatalf("desktop retry returned root %q, err %v; want %q", got.snapshot.Root.Name, got.err, tc.wantRootName)
					}
				}
				wantReads := tc.changeCount + 1
				if wantReads > 2 {
					wantReads = 2 // Snapshot deliberately bounds desktop-root retries.
				}
				if reads.Load() != wantReads {
					t.Fatalf("desktop children read %d times, want %d attempts", reads.Load(), wantReads)
				}
			case <-ctx.Done():
				t.Fatalf("desktop snapshot did not finish: %v", ctx.Err())
			}
		})
	}
}
