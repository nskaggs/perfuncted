package accessibility

import "testing"

// A provider's malformed self-referential relations cannot substitute a
// target's own text for the independent label evidence requested by a caller.
func TestSemanticSelectorRequiresDistinctLabelNode(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/input", Generation: 8}
	labelID := NodeID{BusName: targetID.BusName, ObjectPath: "/label", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Account password", Relations: map[string][]NodeID{"labelled-by": {targetID}, "label-for": {targetID}}}
	selector := Selector{Role: "entry", Label: "password"}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-referential label accepted target name as label evidence: %+v", got)
	}
	// A label-for edge on the target itself must not count as a reverse label.
	target.Relations = map[string][]NodeID{"label-for": {targetID}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-referential label-for edge accepted target name: %+v", got)
	}
	// A separate accessible label remains sufficient in either direction.
	target.Relations = map[string][]NodeID{"labelled-by": {labelID}}
	label := Node{ID: labelID, Role: "label", Text: "Password field"}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, label}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid external labelled-by evidence rejected: %+v", got)
	}
	target.Relations = nil
	label.Relations = map[string][]NodeID{"label-for": {targetID}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, label}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid external label-for evidence rejected: %+v", got)
	}
}
