package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSnapshotRejectsGenerationInvalidatedDuringCacheLoad(t *testing.T) {
	backend, provider := newPrivateCacheBackend(t, syntheticCacheItems())
	root := NodeID{BusName: syntheticCacheService, ObjectPath: "/app", Generation: backend.Generation(), Incarnation: 1}
	opts := SnapshotOptions{}.normalized()
	backend.mu.Lock()
	if backend.cache == nil {
		backend.cache = make(map[string]cachedSnapshot)
	}
	backend.cache[snapshotKey(root, opts)] = cachedSnapshot{
		at: time.Now(),
		snapshot: Snapshot{
			Root:       Node{ID: root, Name: "snapshot from the retired generation"},
			Nodes:      []Node{{ID: root, Name: "snapshot from the retired generation"}},
			Generation: root.Generation,
		},
	}
	backend.mu.Unlock()
	provider.mu.Lock()
	provider.beforeReturn = func() { backend.Invalidate(root) }
	provider.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshot, err := backend.Snapshot(ctx, root, SnapshotOptions{})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("snapshot during cache generation change = %+v, %v; want stale-generation error", snapshot, err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.Name != "" {
		t.Fatalf("snapshot exposed retired cache data: %+v", snapshot)
	}
	if len(backend.cache) != 0 || len(backend.cacheItems) != 0 || len(backend.cacheApps) != 0 {
		t.Fatalf("generation change retained local or provider cache state: snapshots=%d items=%d apps=%d", len(backend.cache), len(backend.cacheItems), len(backend.cacheApps))
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("cache provider calls = %d, want exactly one in-flight read", calls)
	}
}
