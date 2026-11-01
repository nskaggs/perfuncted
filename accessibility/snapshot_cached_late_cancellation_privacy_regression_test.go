package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A cached snapshot contains private accessibility fields. A caller that
// canceled while the backend was validating connectivity must not receive
// that cached data merely because the cache lookup itself is fast.
func TestATSPICachedSnapshotDoesNotPublishAfterCancellationDuringAdmission(t *testing.T) {
	b, root, _ := newSyntheticATSPIBackend(t, false)
	opts := SnapshotOptions{}
	previous, err := b.Snapshot(context.Background(), root, opts)
	if err != nil || len(previous.Nodes) == 0 {
		t.Fatalf("fixture did not populate a real cached snapshot: nodes=%d err=%v", len(previous.Nodes), err)
	}
	b.mu.RLock()
	_, cached := b.cache[snapshotKey(root, opts.normalized())]
	b.mu.RUnlock()
	if !cached {
		t.Fatal("fixture missing cached snapshot entry")
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedEventContext{Context: base, firstRead: make(chan struct{})}
	type result struct {
		snapshot Snapshot
		err      error
	}
	out := make(chan result, 1)
	b.mu.Lock()
	go func() { snapshot, err := b.Snapshot(ctx, root, opts); out <- result{snapshot, err} }()
	select {
	case <-ctx.firstRead:
	case <-time.After(time.Second):
		b.mu.Unlock()
		t.Fatal("snapshot admission did not inspect caller cancellation")
	}
	cancel()
	b.mu.Unlock()
	select {
	case value := <-out:
		if !errors.Is(value.err, context.Canceled) || len(value.snapshot.Nodes) != 0 || value.snapshot.Root.Name != "" {
			t.Fatalf("canceled cached snapshot exposed private nodes: root=%q nodes=%d err=%v", value.snapshot.Root.Name, len(value.snapshot.Nodes), value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled snapshot did not terminate")
	}
}
