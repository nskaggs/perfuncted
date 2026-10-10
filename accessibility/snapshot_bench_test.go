package accessibility

import (
	"fmt"
	"testing"
	"time"
)

// benchmarkNodeCount is the tree size the snapshot benchmarks exercise. Real
// application trees routinely reach and exceed this, which is what pushes the
// default 1 MiB response budget into truncation.
const benchmarkNodeCount = 2000

// buildBenchmarkSnapshot produces a tree with the shape of a real application:
// a root, a handful of framed children, and mostly leaf text nodes carrying
// names, roles, states, and bounds.
func buildBenchmarkSnapshot() Snapshot {
	nodeCount := benchmarkNodeCount
	const busName = "org.test.App"
	rootID := NodeID{BusName: busName, ObjectPath: "/app", Generation: 1}

	nodes := make([]Node, 0, nodeCount)
	nodes = append(nodes, Node{
		ID: rootID, Role: "application", Name: "Test Application",
		States: []string{"focused", "visible"}, Bounds: Rect{X: 0, Y: 0, Width: 1920, Height: 1080},
		HasBounds: true, Visible: true, Showing: true, Enabled: true, ChildCount: 1,
	})
	for i := range nodeCount - 1 {
		path := fmt.Sprintf("/app/node-%04d", i)
		id := NodeID{BusName: busName, ObjectPath: path, Generation: 1}
		role := "text"
		if i%17 == 0 {
			role = "push button"
		}
		name := fmt.Sprintf("Label %d with some descriptive text", i)
		nodes = append(nodes, Node{
			ID: id, Parent: rootID, Role: role, Name: name,
			Description: "A longer description string used to estimate serialized size",
			States:      []string{"visible", "showing"},
			Text:        fmt.Sprintf("Some editable text content for node %d that runs on a bit", i),
			Bounds:      Rect{X: i % 1800, Y: i % 900, Width: 320, Height: 48},
			HasBounds:   true,
			Visible:     true,
			Showing:     true,
			Enabled:     true,
			ChildCount:  0,
			Interfaces:  []string{"org.a11y.atspi.Text", "org.a11y.atspi.Component"},
			Attributes:  map[string]string{"toolkit": "gtk4", "version": "4.14"},
			Children:    nil,
		})
	}
	nodes[0].Children = []NodeID{nodes[1].ID}
	return Snapshot{
		Root: nodes[0], Nodes: nodes, Generation: 1,
		CapturedAt: time.Unix(1750000000, 0).UTC(), Source: "provider-cache",
	}
}

// A snapshot that fits the byte budget takes the single-pass path.
func BenchmarkBoundSnapshotResponseWithinBudget(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	const budget = 8 << 20
	const maxAllocations = 4500
	allocations := testing.AllocsPerRun(5, func() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse allocation check: %v", err)
		}
	})
	if allocations > float64(maxAllocations) {
		b.Fatalf("in-budget snapshot allocations = %.1f, want at most %d", allocations, maxAllocations)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bounded, err := boundSnapshotResponse(snapshot, budget)
		if err != nil {
			b.Fatalf("boundSnapshotResponse: %v", err)
		}
		if len(bounded.Nodes) != len(snapshot.Nodes) {
			b.Fatalf("nodes = %d, want %d", len(bounded.Nodes), len(snapshot.Nodes))
		}
	}
}

// An over-budget snapshot searches node prefixes and accounts for references
// removed from each candidate.
func BenchmarkBoundSnapshotResponseTruncating(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	const budget = 48 << 10
	const maxAllocations = 400
	allocations := testing.AllocsPerRun(5, func() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse allocation check: %v", err)
		}
	})
	if allocations > float64(maxAllocations) {
		b.Fatalf("truncated snapshot allocations = %.1f, want at most %d", allocations, maxAllocations)
	}
	projected, err := boundSnapshotResponse(snapshot, budget)
	if err != nil {
		b.Fatalf("boundSnapshotResponse metric sample: %v", err)
	}
	responseBytes := snapshotJSONSize(projected)
	if responseBytes > budget {
		b.Fatalf("truncated response size = %d, want at most %d", responseBytes, budget)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse: %v", err)
		}
	}
	b.ReportMetric(float64(len(snapshot.Nodes)), "input_nodes/op")
	b.ReportMetric(float64(len(projected.Nodes)), "returned_nodes/op")
	b.ReportMetric(float64(responseBytes), "response_bytes/op")
}

func BenchmarkSnapshotJSONSize(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	b.ReportAllocs()
	for b.Loop() {
		if snapshotJSONSize(snapshot) == 0 {
			b.Fatal("estimated size is zero")
		}
	}
}

func BenchmarkPruneSnapshotReferences(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	b.ReportAllocs()
	for b.Loop() {
		candidate := snapshot
		candidate.Nodes = append([]Node(nil), snapshot.Nodes...)
		pruneSnapshotReferences(&candidate)
	}
}

func BenchmarkSnapshotKey(b *testing.B) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 3, Incarnation: 1}
	opts := SnapshotOptions{MaxDepth: 20, MaxNodes: 4000, MaxTextBytes: 4096, MaxTotalBytes: 8 << 20}
	b.ReportAllocs()
	for b.Loop() {
		if snapshotKey(id, opts) == "" {
			b.Fatal("empty snapshot key")
		}
	}
}
