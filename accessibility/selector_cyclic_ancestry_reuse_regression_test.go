package accessibility

import "testing"

// A malformed AT-SPI parent cycle cannot supply distinct ancestors by
// traversing the same object twice. Ordered ancestor constraints require an
// actual finite chain above the target, not a repeated semantic identity.
func TestSemanticSelectorRejectsRepeatedAncestorFromParentCycle(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 7}
	containerID := NodeID{BusName: targetID.BusName, ObjectPath: "/container", Generation: 7}
	intermediateID := NodeID{BusName: targetID.BusName, ObjectPath: "/middle", Generation: 7}
	target := Node{ID: targetID, Role: "entry", Parent: containerID}
	container := Node{ID: containerID, Role: "group", Name: "Compose", Parent: intermediateID}
	middle := Node{ID: intermediateID, Role: "section", Name: "Details", Parent: containerID}
	nodes := map[NodeID]Node{targetID: target, containerID: container, intermediateID: middle}
	repeated := []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "group", Name: "Compose"}}
	if matchesAncestors(nodes, target, repeated) {
		t.Fatal("cyclic AT-SPI parent chain re-used the same Compose group for two required ancestors")
	}
	if matchesAncestors(nodes, target, []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "section", Name: "Details"}, {Role: "group", Name: "Compose"}}) {
		t.Fatal("cyclic AT-SPI parent chain re-used an ancestor after visiting a different parent")
	}
	// A real distinct outer group must still authorize repeated constraints.
	outerID := NodeID{BusName: targetID.BusName, ObjectPath: "/outer", Generation: 7}
	middle.Parent = outerID
	nodes[intermediateID] = middle
	nodes[outerID] = Node{ID: outerID, Role: "group", Name: "Compose"}
	if !matchesAncestors(nodes, target, repeated) {
		t.Fatal("two distinct Compose ancestors were rejected")
	}
	if !matchesAncestors(nodes, target, []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "section", Name: "Details"}, {Role: "group", Name: "Compose"}}) {
		t.Fatal("acyclic ordered ancestor chain was rejected")
	}
}

// Semantic discovery must uphold the same ancestry identity rule when it
// filters a captured graph, not only when the low-level predicate is called.
func TestFilterSnapshotSelectorDoesNotPublishCyclicAncestorMatch(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 8}
	groupID := NodeID{BusName: targetID.BusName, ObjectPath: "/group", Generation: 8}
	sectionID := NodeID{BusName: targetID.BusName, ObjectPath: "/section", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Address", Parent: groupID}
	group := Node{ID: groupID, Role: "group", Name: "Compose", Parent: sectionID}
	section := Node{ID: sectionID, Role: "section", Parent: groupID}
	snapshot := Snapshot{Nodes: []Node{target, group, section}}
	selector := Selector{Role: "entry", Name: "Address", Ancestors: []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "group", Name: "Compose"}}}
	if got := FilterSnapshotSelector(snapshot, selector); len(got) != 0 {
		t.Fatalf("cyclic parent data published unsupported semantic match: %+v", got)
	}
	outerID := NodeID{BusName: targetID.BusName, ObjectPath: "/outer", Generation: 8}
	snapshot.Nodes[2].Parent = outerID
	snapshot.Nodes = append(snapshot.Nodes, Node{ID: outerID, Role: "group", Name: "Compose"})
	if got := FilterSnapshotSelector(snapshot, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("finite, distinct ancestor chain could not select target: %+v", got)
	}
}

// The selected object can never be its own ancestor, even when a broken AT-SPI
// provider supplies a self-parent pointer or a cycle back through a container.
func TestFilterSnapshotSelectorDoesNotTreatTargetAsItsOwnAncestor(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 8}
	parentID := NodeID{BusName: targetID.BusName, ObjectPath: "/parent", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Name", Parent: targetID}
	selector := Selector{Role: "entry", Name: "Name", Ancestors: []AncestorSelector{{Role: "entry", Name: "Name"}}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-parenting target satisfied its own ancestor constraint: %+v", got)
	}
	target.Parent = parentID
	parent := Node{ID: parentID, Role: "group", Name: "Form", Parent: targetID}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, parent}}, selector); len(got) != 0 {
		t.Fatalf("target reappeared as ancestor after a parent cycle: %+v", got)
	}
	// Non-cyclic parent chains must still resolve ordinary semantic selectors.
	parent.Parent = NodeID{}
	selector.Ancestors = []AncestorSelector{{Role: "group", Name: "Form"}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, parent}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid parent chain unexpectedly rejected: %+v", got)
	}
}
