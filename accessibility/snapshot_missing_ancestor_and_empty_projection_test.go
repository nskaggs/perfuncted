package accessibility

import (
	"encoding/json"
	"testing"
)

// A selector must never infer ancestry through a parent ID whose node was
// absent from its snapshot: an unobserved ancestor cannot prove containment.
func TestSelectorMissingAncestorCannotAuthorizeDescendantMatch(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 4}
	group := NodeID{BusName: root.BusName, ObjectPath: "/missing-group", Generation: 4}
	leaf := Node{ID: NodeID{BusName: root.BusName, ObjectPath: "/button", Generation: 4}, Parent: group, Name: "Submit", Role: "push-button"}
	byID := map[NodeID]Node{leaf.ID: leaf, root: {ID: root, Role: "frame", Name: "Editor"}}
	constraints := []AncestorSelector{{Role: "frame", Name: "Editor"}}
	if matchesAncestors(byID, leaf, constraints) {
		t.Fatal("unobserved parent chain authorized descendant selector")
	}
	byID[group] = Node{ID: group, Parent: root, Role: "group", Name: "Form"}
	if !matchesAncestors(byID, leaf, constraints) {
		t.Fatal("complete observed parent chain rejected")
	}
	delete(byID, root)
	if matchesAncestors(byID, leaf, constraints) {
		t.Fatal("missing far ancestor authorized descendant selector")
	}
}

// An empty but allocated node slice must serialize as [] rather than null.
// The streaming size estimator must agree with actual JSON output even when
// no nodes are retained after snapshot filtering.
func TestSnapshotEmptyAllocatedNodeListSizeMatchesEncoding(t *testing.T) {
	snapshot := Snapshot{Nodes: make([]Node, 0)}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotJSONSize(snapshot) != len(encoded) {
		t.Fatalf("empty node list byte estimate=%d JSON=%d: %s", snapshotJSONSize(snapshot), len(encoded), encoded)
	}
}
