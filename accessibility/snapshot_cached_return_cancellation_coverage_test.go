package accessibility

import (
	"context"
	"errors"
	"testing"
)

type cancelOnContextErrorRead struct {
	context.Context
	cancel    context.CancelFunc
	cancelAt  int
	errorRead int
}

func (c *cancelOnContextErrorRead) Err() error {
	c.errorRead++
	if c.errorRead == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestATSPICachedSnapshotRejectsCancellationAtReturnBoundary(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	defer backend.Close()
	options := SnapshotOptions{}
	if snapshot, err := backend.Snapshot(context.Background(), root, options); err != nil || len(snapshot.Nodes) == 0 {
		t.Fatalf("populate cached snapshot: nodes=%d err=%v", len(snapshot.Nodes), err)
	}
	key := snapshotKey(root, options.normalized())
	backend.mu.RLock()
	_, cached := backend.cache[key]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("snapshot fixture did not publish its cache entry")
	}

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Snapshot checks cancellation during admission, then once more after it
	// clones the cache entry and before it returns that private data.
	ctx := &cancelOnContextErrorRead{Context: base, cancel: cancel, cancelAt: 3}
	snapshot, err := backend.Snapshot(ctx, root, options)
	if !errors.Is(err, context.Canceled) || len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("canceled cache hit exposed snapshot data: nodes=%d root=%+v err=%v", len(snapshot.Nodes), snapshot.Root, err)
	}
	if ctx.errorRead != ctx.cancelAt {
		t.Fatalf("cancellation observed at context error read %d, want return-boundary read %d", ctx.errorRead, ctx.cancelAt)
	}
	backend.mu.RLock()
	_, cached = backend.cache[key]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("caller cancellation mutated the backend's valid cached snapshot")
	}
}
