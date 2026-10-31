package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Outline must route through the same scoped, redacting snapshot as ordinary
// automation. A provider that marks a subtree sensitive may not disclose
// its text through this alternative diagnostic API.
func TestSyntheticATSPIBackendOutlineKeepsSnapshotSecurityAndScope(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, true)
	opts := SnapshotOptions{Fresh: true, MaxDepth: 3, MaxNodes: 3}
	outline, err := backend.Outline(context.Background(), root, opts, OutlineOptions{MaxDepth: 2, MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Root.ID != root || outline.Generation != root.Generation || outline.Root.Text != "" {
		t.Fatalf("outline bypassed snapshot privacy or root scope: %+v", outline)
	}
	allowed, err := backend.Outline(context.Background(), root, SnapshotOptions{Fresh: true, AllowSensitive: true}, OutlineOptions{MaxDepth: 3, MaxNodes: 3})
	if err != nil || allowed.Root.Text != "sensitive text" {
		t.Fatalf("explicitly authorized outline: %+v err=%v", allowed, err)
	}
	if _, err := backend.Outline(context.Background(), NodeID{}, opts, OutlineOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped outline bypassed snapshot scope: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Outline(canceled, root, opts, OutlineOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled diagnostic outline: %v", err)
	}
	backend.Invalidate(root)
	if _, err := backend.Outline(context.Background(), root, opts, OutlineOptions{}); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("outline accepted stale provider object: %v", err)
	}
}
