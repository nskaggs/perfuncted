package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSnapshotRejectsUnavailableAndInvalidRequests(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.Snapshot(context.Background(), NodeID{}, SnapshotOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend snapshot error = %v", err)
	}

	backend, root, _ := newSyntheticATSPIBackend(t, false)
	if _, err := backend.Snapshot(nil, root, SnapshotOptions{}); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil-context snapshot error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Snapshot(canceled, root, SnapshotOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot error = %v", err)
	}
	invalid := NodeID{BusName: root.BusName, Generation: root.Generation}
	if _, err := backend.Snapshot(context.Background(), invalid, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid root snapshot error = %v", err)
	}
	if _, err := backend.Find(context.Background(), invalid, Query{}, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid root find error = %v", err)
	}
	backend.Invalidate(root)
	if _, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true}); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("invalidated root snapshot error = %v", err)
	}
}

func TestSnapshotReturnsProviderFailureAsIncompleteEvidence(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/missing"}}
		frame.childrenFailure = "synthetic child provider unavailable"
	})

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if err != nil {
		t.Fatalf("partial provider snapshot should preserve observed root: %v", err)
	}
	if snapshot.Root.ID != root || len(snapshot.Nodes) != 1 || snapshot.ProviderErrors != 1 || len(snapshot.Warnings) != 1 {
		t.Fatalf("provider failure was not represented as partial evidence: %+v", snapshot)
	}
	if err := ValidateSemanticSnapshot(snapshot, Selector{Role: "frame"}); !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("partial provider snapshot was treated as authoritative: %v", err)
	}
}

func TestSnapshotResponseBudgetRefusesWithoutPublishing(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	opts := SnapshotOptions{Fresh: true, MaxTotalBytes: 1}
	if _, err := backend.Snapshot(context.Background(), root, opts); !errors.Is(err, ErrResponseBudget) {
		t.Fatalf("below-minimum snapshot budget error = %v", err)
	}
	if _, ok := backend.cache[snapshotKey(root, opts.normalized())]; ok {
		t.Fatal("snapshot that exceeded its minimum response budget was published")
	}
}

func TestFindReturnsMatchesFromOneBoundedSnapshot(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	matches, err := backend.Find(context.Background(), root, Query{Name: "document", Role: "frame"}, SnapshotOptions{Fresh: true})
	if err != nil || len(matches) != 1 || matches[0].ID != root {
		t.Fatalf("Find over a fresh snapshot = %+v, %v", matches, err)
	}
}

func TestFocusedPropagatesSnapshotFailureAndReportsNoMatch(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.Focused(context.Background(), SnapshotOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("focused lookup on disconnected backend = %v", err)
	}
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.states = nil
	})
	if _, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused lookup without a focused node = %v", err)
	}
}

func TestAtPointValidatesCoordinatesAndUsesProviderResult(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.AtPoint(context.Background(), 0, 0); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("point lookup on disconnected backend = %v", err)
	}
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
		desktop.pointResult = objectRef{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/editor"}
	})
	if _, err := backend.AtPoint(nil, 0, 0); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil-context point lookup = %v", err)
	}
	if _, err := backend.AtPoint(context.Background(), int(^uint(0)>>1), 0); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("out-of-range point lookup = %v", err)
	}
	node, err := backend.AtPoint(context.Background(), 10, 15)
	if err != nil || node.ID != root || node.Name != "Editor — document" {
		t.Fatalf("point lookup returned %+v, %v", node, err)
	}
	backend, _, _ = newSyntheticATSPIBackend(t, false)
	if _, err := backend.AtPoint(context.Background(), 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("null point result = %v", err)
	}
	backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
		desktop.pointFailure = "synthetic component failure"
	})
	if _, err := backend.AtPoint(context.Background(), 1, 1); err == nil || !strings.Contains(err.Error(), "point query") {
		t.Fatalf("provider point failure = %v", err)
	}
}

func TestCloneSnapshotOwnsTypedMetadataAndBoundReasonsStayUnique(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/editor", Generation: 1}
	child := NodeID{BusName: root.BusName, ObjectPath: "/label", Generation: root.Generation}
	in := Snapshot{Root: Node{ID: root}, Nodes: []Node{{
		ID:        root,
		Value:     &ValueInfo{Current: 1, Minimum: 0, Maximum: 10, MinimumIncrement: 0.5},
		Selection: &SelectionInfo{SelectedChildCount: 1},
		Table:     &TableInfo{Rows: 2, Columns: 3},
		Document:  &DocumentInfo{Locale: "en", CurrentPageNumber: 1, PageCount: 2},
		Actions:   []Action{{Name: "activate"}},
		Relations: map[string][]NodeID{"labelled-by": {child}},
	}}}
	out := cloneSnapshot(in)
	out.Nodes[0].Value.Current = 8
	out.Nodes[0].Selection.SelectedChildCount = 5
	out.Nodes[0].Table.Rows = 8
	out.Nodes[0].Document.Locale = "fr"
	out.Nodes[0].Actions[0].Name = "changed"
	out.Nodes[0].Relations["labelled-by"][0] = NodeID{}
	if in.Nodes[0].Value.Current != 1 || in.Nodes[0].Selection.SelectedChildCount != 1 || in.Nodes[0].Table.Rows != 2 || in.Nodes[0].Document.Locale != "en" || in.Nodes[0].Actions[0].Name != "activate" || in.Nodes[0].Relations["labelled-by"][0] != child {
		t.Fatal("snapshot clone shares typed or collection metadata with its source")
	}

	maxBytes := 1024
	snapshot := Snapshot{
		Root:              Node{ID: root, Name: strings.Repeat("long accessible name ", 512)},
		Nodes:             []Node{{ID: root, Name: strings.Repeat("long accessible name ", 512)}},
		TruncationReasons: []string{"max total bytes 1024"},
	}
	bounded, err := boundSnapshotResponse(snapshot, maxBytes)
	if err != nil {
		t.Fatalf("bound oversized snapshot: %v", err)
	}
	if got := snapshotJSONSize(bounded); got > maxBytes {
		t.Fatalf("bounded response has %d bytes, limit %d", got, maxBytes)
	}
	if len(bounded.TruncationReasons) != 1 || bounded.TruncationReasons[0] != "max total bytes 1024" {
		t.Fatalf("response duplicated its truncation reason: %v", bounded.TruncationReasons)
	}
}

func TestSnapshotJSONHelpersSaturateAndPreserveUTF8(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if got := addJSONSize(maxInt-1, 2); got != maxInt {
		t.Fatalf("overflowing JSON size = %d, want saturated %d", got, maxInt)
	}
	if got := addJSONSize(12, -4); got != 8 {
		t.Fatalf("non-overflowing signed JSON size = %d, want 8", got)
	}
	for _, tc := range []struct {
		value string
		limit int
		want  string
	}{
		{value: "é", limit: 1, want: ""},
		{value: "é", limit: -1, want: ""},
		{value: "é", limit: 2, want: "é"},
	} {
		got, truncated := truncateUTF8(tc.value, tc.limit)
		if got != tc.want || truncated != (len(tc.value) > tc.limit) {
			t.Fatalf("truncateUTF8(%q, %d) = %q, %t", tc.value, tc.limit, got, truncated)
		}
	}
}
