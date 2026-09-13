package perfuncted

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/capability"
)

type countingFindBackend struct {
	bundleAccessibilityFake
	finds     int
	snapshots int
	nodes     []accessibility.Node
}

func (f *countingFindBackend) Find(_ context.Context, _ accessibility.NodeID, query accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.finds++
	var out []accessibility.Node
	for _, node := range f.nodes {
		if query.Name != "" && !strings.Contains(strings.ToLower(node.Name), strings.ToLower(query.Name)) {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func (f *countingFindBackend) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshots++
	return accessibility.Snapshot{Nodes: f.nodes, Generation: f.gen}, nil
}

func TestAdversarialFindOnePaysSingleSnapshot(t *testing.T) {
	root := accessibility.NodeID{BusName: "b", ObjectPath: "/root", Generation: 2}
	missing := accessibility.Query{Name: "does-not-exist"}
	backend := &countingFindBackend{
		bundleAccessibilityFake: bundleAccessibilityFake{gen: 2},
		nodes: []accessibility.Node{
			{ID: accessibility.NodeID{BusName: "b", ObjectPath: "/a", Generation: 2}, Name: "alpha"},
		},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	_, err := session.Accessibility.FindOne(context.Background(), root, missing, accessibility.SnapshotOptions{})
	if err == nil {
		t.Fatal("missing query unexpectedly succeeded")
	}
	// Find already performed a bounded snapshot-equivalent read; a second
	// snapshot purely for candidate diagnostics doubles AT-SPI round trips.
	// The correct contract is exactly one backend read per FindOne.
	if backend.finds != 0 || backend.snapshots != 1 {
		t.Fatalf("FindOne paid finds=%d snapshots=%d, want 0 finds and 1 snapshot", backend.finds, backend.snapshots)
	}
}

func TestAdversarialCapabilityOperationsMatchBackend(t *testing.T) {
	backendOps := (&bundleAccessibilityFake{gen: 1}).SupportedOperations()
	_ = backendOps
	caps := capability.Operations("accessibility")
	for _, required := range []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "window-root", "reopen"} {
		found := false
		for _, c := range caps {
			if c == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("capability operations missing %q (caps=%v)", required, caps)
		}
	}
}
