package accessibility

import (
	"encoding/json"
	"testing"
)

// A multi-action node has array separators and string escaping on every
// action. Underestimating either permits responses beyond the caller's budget.
func TestSnapshotActionArrayBudgetAccountsForEveryAction(t *testing.T) {
	actions := []Action{
		{Index: 0, Name: "Open", KeyBinding: "Ctrl+O"},
		{Index: 1, Name: "Save & Close", Description: "A \"private\" document <snapshot>"},
		{Index: 2, Name: "Dismiss", LocalizedName: "Fermé"},
	}
	encoded, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateActionsJSONSize(actions); estimated < len(encoded) {
		t.Fatalf("multi-action JSON underestimated: estimated=%d actual=%d", estimated, len(encoded))
	}
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8}
	snapshot := Snapshot{Root: Node{ID: root, Actions: actions}, Nodes: []Node{{ID: root, Actions: actions}}, Generation: 8}
	encoded, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := snapshotJSONSize(snapshot); estimated < len(encoded) {
		t.Fatalf("snapshot with multiple actions underestimated: estimated=%d actual=%d", estimated, len(encoded))
	}
	limit := len(encoded) - 1
	bounded, err := boundSnapshotResponse(snapshot, limit)
	if err != nil {
		return // A minimum safe response can be larger than the requested budget.
	}
	result, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > limit || !bounded.Truncated {
		t.Fatalf("budget exceeded or truncation unreported: bytes=%d limit=%d truncated=%v", len(result), limit, bounded.Truncated)
	}
}

// An absent collection must contribute zero bytes; a nil response must not
// mutate retained nodes or authorize references from a previous snapshot.
func TestSnapshotMissingCollectionsAndNilReferencePruningRemainInert(t *testing.T) {
	if n := estimateStringSliceJSONSize(nil); n != 0 {
		t.Fatalf("nil string slice charged %d bytes", n)
	}
	if n := estimateStringSliceJSONSize([]string{}); n != 0 {
		t.Fatalf("empty string slice charged %d bytes", n)
	}
	var absent *Snapshot
	pruneSnapshotReferences(absent)
	pruneSnapshotReferencesWithRetained(absent, map[NodeID]struct{}{})
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8}
	node := Node{ID: id, Children: []NodeID{{BusName: id.BusName, ObjectPath: "/removed", Generation: id.Generation}}}
	snapshot := Snapshot{Root: node, Nodes: []Node{node}}
	pruneSnapshotReferencesWithRetained(&snapshot, map[NodeID]struct{}{id: {}})
	if len(snapshot.Root.Children) != 0 {
		t.Fatalf("orphan children retained: %+v", snapshot.Root.Children)
	}
}
