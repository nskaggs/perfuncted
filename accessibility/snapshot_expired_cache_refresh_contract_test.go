package accessibility

import (
	"context"
	"testing"
	"time"
)

// A cached accessibility snapshot contains private provider information.
// Expiration and explicit Fresh reads must replace that entry with real
// provider data rather than returning stale or corrupted cache contents.
func TestATSPISnapshotCacheTTLExpiresAndFreshBypassesUnexpiredEntry(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	root := NodeID{}
	desktop := backend.desktop()
	opts := SnapshotOptions{MaxNodes: 3, MaxDepth: 2, AllowDesktopRoot: true}
	ctx := context.Background()
	first, err := backend.Snapshot(ctx, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Root.Name != "Desktop" {
		t.Fatalf("unexpected baseline: %q", first.Root.Name)
	}
	key := snapshotKey(desktop, opts.normalized())
	backend.mu.Lock()
	entry, exists := backend.cache[key]
	if !exists {
		backend.mu.Unlock()
		t.Fatal("successful snapshot was not cached")
	}
	// Deliberately poison the cache only; the D-Bus fixture remains immutable
	// so the concurrent test service never races with test mutations.
	entry.snapshot.Nodes[0].Name = "stale cache content"
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()
	cached, err := backend.Snapshot(ctx, root, opts)
	if err != nil || cached.Root.Name != "stale cache content" {
		t.Fatalf("valid cached entry not reused: %q %v", cached.Root.Name, err)
	}
	backend.mu.Lock()
	entry = backend.cache[key]
	entry.at = time.Now().Add(-2 * cacheTTL)
	backend.cache[key] = entry
	backend.mu.Unlock()
	refreshed, err := backend.Snapshot(ctx, root, opts)
	if err != nil || refreshed.Root.Name != first.Root.Name {
		t.Fatalf("expired cache not refreshed: %q %v", refreshed.Root.Name, err)
	}
	backend.mu.Lock()
	entry = backend.cache[key]
	entry.snapshot.Nodes[0].Name = "stale after refresh"
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()
	freshOpts := opts
	freshOpts.Fresh = true
	fresh, err := backend.Snapshot(ctx, root, freshOpts)
	if err != nil || fresh.Root.Name != first.Root.Name {
		t.Fatalf("Fresh read returned corrupted cached content: %q %v", fresh.Root.Name, err)
	}
	following, err := backend.Snapshot(ctx, root, opts)
	if err != nil || following.Root.Name != first.Root.Name {
		t.Fatalf("Fresh result did not repair shared cache: %q %v", following.Root.Name, err)
	}
}
