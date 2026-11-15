package accessibility

import (
	"errors"
	"slices"
	"testing"
)

// Even when no tree nodes fit the response budget, the fallback must preserve
// the requested root identity while stripping provider details and references.
// The fallback's successful result is distinct from an impossible budget.
func TestSnapshotMinimumBudgetFallbackRetainsOnlyRootIdentity(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/window", Generation: 4, Incarnation: 7}
	child := NodeID{BusName: root.BusName, ObjectPath: "/child", Generation: root.Generation, Incarnation: root.Incarnation}
	source := Snapshot{
		Root:           Node{ID: root, Name: "secret title", Children: []NodeID{child}},
		Nodes:          []Node{{ID: root, Name: "secret title", Children: []NodeID{child}}, {ID: child, Name: "secret child"}},
		Warnings:       []string{"untrusted provider diagnostic"},
		ProviderErrors: 3,
		Generation:     root.Generation,
	}
	minimum, err := minimumSnapshotResponse(source, root, 4096)
	if err != nil {
		t.Fatalf("minimum budget fallback: %v", err)
	}
	if minimum.Root.ID != root || minimum.Root.Name != "" || len(minimum.Root.Children) != 0 || len(minimum.Nodes) != 0 || len(minimum.Warnings) != 0 || minimum.ProviderErrors != 0 {
		t.Fatalf("fallback leaked truncated snapshot content: %+v", minimum)
	}
	if !slices.Equal(source.Warnings, []string{"untrusted provider diagnostic"}) || len(source.Nodes) != 2 || source.Root.Name != "secret title" {
		t.Fatalf("fallback mutated original provider snapshot: %+v", source)
	}
	minimumSize := snapshotJSONSize(minimum)
	if minimumSize > 4096 {
		t.Fatalf("minimum exceeded budget: %d", minimumSize)
	}
	if _, err := minimumSnapshotResponse(source, root, minimumSize-1); !errors.Is(err, ErrResponseBudget) {
		t.Fatalf("impossible minimum budget accepted: %v", err)
	}
}
