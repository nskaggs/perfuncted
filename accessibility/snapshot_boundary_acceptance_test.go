package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyntheticATSPIDesktopSnapshotIsOptInAndCacheOwned(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()

	if _, err := backend.Snapshot(ctx, NodeID{}, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped desktop traversal returned %v", err)
	}

	opts := SnapshotOptions{AllowDesktopRoot: true, MaxNodes: 3}
	snapshot, err := backend.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		t.Fatalf("opted-in desktop snapshot: %v", err)
	}
	if snapshot.Root.ID.ObjectPath != string(desktopPath) || len(snapshot.Nodes) != 3 || snapshot.Truncated {
		t.Fatalf("desktop snapshot has wrong scope or bounds: root=%+v nodes=%d truncated=%t", snapshot.Root.ID, len(snapshot.Nodes), snapshot.Truncated)
	}
	if snapshot.Nodes[0].ID.ObjectPath != string(desktopPath) || snapshot.Nodes[1].Parent != snapshot.Nodes[0].ID || snapshot.Nodes[2].Parent != snapshot.Nodes[1].ID {
		t.Fatalf("desktop traversal lost parent-linked order: %+v", snapshot.Nodes)
	}

	// Make cache reuse deterministic while preserving the production snapshot
	// captured above. Snapshot results are caller-owned and must not mutate it.
	key := snapshotKey(backend.desktop(), opts.normalized())
	backend.mu.Lock()
	entry, ok := backend.cache[key]
	if !ok {
		backend.mu.Unlock()
		t.Fatal("successful desktop snapshot was not published to cache")
	}
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()

	snapshot.Nodes[0].Name = "caller mutation"
	snapshot.Nodes[0].Children[0] = NodeID{}
	snapshot.Nodes[2].Attributes["role"] = "caller mutation"
	cached, err := backend.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		t.Fatalf("cached desktop snapshot: %v", err)
	}
	if cached.Nodes[0].Name != "Desktop" || cached.Nodes[0].Children[0].ObjectPath != "/org/a11y/atspi/accessible/app" || cached.Nodes[2].Attributes["role"] != "editor" {
		t.Fatalf("caller mutation escaped into cached desktop snapshot: %+v", cached.Nodes)
	}
}

func TestBoundNodeResponsePrunesOptionalFieldsWithinBudget(t *testing.T) {
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/editor", Generation: 1}
	tests := []struct {
		name  string
		apply func(*Node)
		check func(Node) bool
	}{
		{"text", func(n *Node) { n.Text = "界面 content repeated界面 content repeated" }, func(n Node) bool {
			return n.TextTruncated && len(n.Text) < len("界面 content repeated界面 content repeated")
		}},
		{"attributes", func(n *Node) { n.Attributes = map[string]string{"label": "an optional accessible label"} }, func(n Node) bool { return n.Attributes == nil }},
		{"relations", func(n *Node) {
			n.Relations = map[string][]NodeID{"labelled-by": {{BusName: id.BusName, ObjectPath: "/label", Generation: id.Generation}}}
		}, func(n Node) bool { return n.Relations == nil }},
		{"actions", func(n *Node) { n.Actions = []Action{{Index: 0, Name: "activate", Description: "activate editor"}} }, func(n Node) bool { return n.Actions == nil }},
		{"warnings", func(n *Node) { n.Warnings = []string{"provider returned optional metadata warning"} }, func(n Node) bool { return n.Warnings == nil }},
		{"description", func(n *Node) { n.Description = "an optional verbose description" }, func(n Node) bool { return n.Description == "" }},
		{"name", func(n *Node) { n.Name = "an optional verbose accessible name" }, func(n Node) bool { return n.Name == "" }},
		{"interfaces", func(n *Node) { n.Interfaces = []string{"org.example.Interface", "org.example.OptionalInterface"} }, func(n Node) bool { return n.Interfaces == nil }},
		{"states", func(n *Node) { n.States = []string{"enabled", "focused", "visible", "showing"} }, func(n Node) bool { return n.States == nil }},
		{"children", func(n *Node) {
			n.Children = []NodeID{{BusName: id.BusName, ObjectPath: "/child", Generation: id.Generation}}
		}, func(n Node) bool { return n.Children == nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := Node{ID: id}
			limitNode := Node{ID: id}
			if tt.name == "text" {
				limitNode.TextTruncated = true
			}
			tt.apply(&node)
			limit := estimateNodeJSONSize(limitNode) + 60
			got := boundNodeResponse(node, limit)
			if !tt.check(got) {
				t.Fatalf("budget pruning retained optional %s: %+v", tt.name, got)
			}
			if got.ID != id {
				t.Fatalf("budget pruning changed stable node identity: %+v", got.ID)
			}
			if estimateNodeJSONSize(got)+60 > limit {
				t.Fatalf("projected node exceeds response budget: size=%d limit=%d", estimateNodeJSONSize(got)+60, limit)
			}
		})
	}
}

func TestATSPIEventsRejectRetiredGeneration(t *testing.T) {
	_, backend, _ := startSyntheticEventBus(t, nil)
	if err := backend.RetireEventsForReopen(context.Background()); err != nil {
		t.Fatalf("retire event generation: %v", err)
	}
	stream, err := backend.Events(context.Background(), EventOptions{})
	if stream != nil || !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired event generation admitted subscriber: stream=%v err=%v", stream, err)
	}
}
