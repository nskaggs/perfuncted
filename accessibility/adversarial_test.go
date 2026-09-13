package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/capability"
)

// Counting backend proves FindOne-style workflows pay two snapshots when one
// suffices. The bundle must resolve a query from a single bounded snapshot.
type countingSnapshotBackend struct {
	snapshots int
	finds     int
	snapshot  Snapshot
}

func (f *countingSnapshotBackend) SupportedOperations() []string {
	return (&dbusBackend{}).SupportedOperations()
}
func (f *countingSnapshotBackend) Applications(context.Context) ([]Application, error) {
	return nil, ErrUnsupported
}
func (f *countingSnapshotBackend) Snapshot(context.Context, NodeID, SnapshotOptions) (Snapshot, error) {
	f.snapshots++
	return f.snapshot, nil
}
func (f *countingSnapshotBackend) Find(_ context.Context, _ NodeID, query Query, _ SnapshotOptions) ([]Node, error) {
	f.finds++
	var out []Node
	for _, node := range f.snapshot.Nodes {
		if query.Name != "" && !strings.Contains(strings.ToLower(node.Name), strings.ToLower(query.Name)) {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}
func (f *countingSnapshotBackend) Focused(context.Context, SnapshotOptions) (Node, error) {
	return Node{}, ErrNotFound
}
func (f *countingSnapshotBackend) AtPoint(context.Context, int, int) (Node, error) {
	return Node{}, ErrNotFound
}
func (f *countingSnapshotBackend) Close() error { return nil }

func TestAdversarialSnapshotBudgetIsEnforced(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 1}
	nodes := make([]Node, 0, 8)
	for i := 0; i < 8; i++ {
		nodes = append(nodes, Node{ID: NodeID{BusName: "org.test", ObjectPath: "/n", Generation: 1}, Name: strings.Repeat("x", 4096)})
	}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: nodes, Generation: 1}
	bounded, err := boundSnapshotResponse(snapshot, 1024)
	if err != nil {
		if !errors.Is(err, ErrResponseBudget) {
			t.Fatalf("budget error = %v", err)
		}
		return
	}
	if snapshotJSONSize(bounded) > 1024 {
		t.Fatalf("bounded snapshot exceeds budget: %d > 1024", snapshotJSONSize(bounded))
	}
}

func TestAdversarialCloneSnapshotIsolatesCache(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 3}
	original := Snapshot{
		Root:       Node{ID: root, Name: "root", Attributes: map[string]string{"class": "field"}},
		Nodes:      []Node{{ID: root, Name: "root", Attributes: map[string]string{"class": "field"}, Children: []NodeID{root}}},
		Generation: 3,
	}
	cloned := cloneSnapshot(original)
	cloned.Nodes[0].Name = "mutated"
	cloned.Nodes[0].Attributes["class"] = "mutated"
	cloned.Nodes[0].Children[0] = NodeID{}
	if original.Nodes[0].Name == "mutated" || original.Nodes[0].Attributes["class"] == "mutated" || original.Nodes[0].Children[0] == (NodeID{}) {
		t.Fatal("cloneSnapshot shares mutable state with cached snapshot")
	}
}

func TestAdversarialShortAccessibleNameMustNotCorrelate(t *testing.T) {
	target := WindowTarget{ID: "w", Title: "Editor - Preferences", PID: 7}
	node := Node{Name: "Edit", Role: "frame", Bounds: Rect{X: 0, Y: 0, Width: 100, Height: 100}, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score != 0 {
		t.Fatalf("generic 4-char accessible name correlated with unrelated title: score=%d", score)
	}
}

func TestAdversarialPrefixSiblingsAreAmbiguous(t *testing.T) {
	target := WindowTarget{ID: "w", Title: "Terminal", PID: 9}
	first := Node{ID: NodeID{BusName: "b", ObjectPath: "/a", Generation: 1}, Role: "frame", Name: "Terminal - a"}
	second := Node{ID: NodeID{BusName: "b", ObjectPath: "/b", Generation: 1}, Role: "frame", Name: "Terminal - b"}
	firstScore, secondScore := windowCandidateScore(first, target), windowCandidateScore(second, target)
	if firstScore == 0 || secondScore == 0 {
		t.Fatalf("prefix siblings discarded: %d vs %d", firstScore, secondScore)
	}
	if firstScore != secondScore {
		t.Fatalf("prefix siblings scored differently: %d vs %d, ambiguity would be hidden", firstScore, secondScore)
	}
	selected, _, ambiguous := chooseWindowCandidate([]windowCandidate{{node: first, score: firstScore}, {node: second, score: secondScore}})
	if selected != nil || !ambiguous {
		t.Fatalf("prefix siblings did not report ambiguity: selected=%+v ambiguous=%v", selected, ambiguous)
	}
}

func TestAdversarialStalePublicationNeverCaches(t *testing.T) {
	backend := &dbusBackend{generation: 8}
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 8}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}, Generation: 8}
	backend.Invalidate(root)
	if err := backend.publishSnapshot("k", root.Generation, snapshot); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale publish = %v, want ErrStaleGeneration", err)
	}
	if len(backend.cache) != 0 {
		t.Fatal("stale snapshot entered cache")
	}
}

func TestAdversarialOperationsAreSingleSource(t *testing.T) {
	ops := (&dbusBackend{}).SupportedOperations()
	caps := capability.Operations("accessibility")
	if len(ops) != len(caps) {
		t.Fatalf("backend ops %d != capability ops %d", len(ops), len(caps))
	}
	for i := range ops {
		if ops[i] != caps[i] {
			t.Fatalf("op %d: backend %q != capability %q", i, ops[i], caps[i])
		}
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op == "" {
			t.Fatal("empty operation name")
		}
		if seen[op] {
			t.Fatalf("duplicate operation %q", op)
		}
		seen[op] = true
	}
	for _, required := range []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "window-root", "reopen"} {
		if !seen[required] {
			t.Fatalf("operations missing %q: %v", required, ops)
		}
	}
}
