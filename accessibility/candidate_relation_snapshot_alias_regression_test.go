package accessibility

import "testing"

// Diagnostics are returned to callers for inspection and JSON serialization.
// Mutating a candidate must never modify the provider snapshot or its other
// candidate results, including nested relation target slices.
func TestSemanticCandidatesDoNotAliasProviderRelationMapsAndTargets(t *testing.T) {
	id := NodeID{BusName: "org.example", ObjectPath: "/node", Generation: 7}
	target := NodeID{BusName: "org.example", ObjectPath: "/label", Generation: 7}
	other := NodeID{BusName: "org.example", ObjectPath: "/other", Generation: 7}
	snapshot := Snapshot{Nodes: []Node{{ID: id, Name: "Editor", Relations: map[string][]NodeID{"labelled-by": {target, other}}}}}
	first := CandidatesForQuery(snapshot, Query{Name: "Editor"}, 1)
	second := CandidatesForQuery(snapshot, Query{Name: "Editor"}, 1)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("missing candidates: first=%v second=%v", first, second)
	}
	first[0].Relations["labelled-by"][0] = id
	first[0].Relations["labelled-by"] = append(first[0].Relations["labelled-by"], id)
	first[0].Relations["new-relation"] = []NodeID{id}
	if got := snapshot.Nodes[0].Relations["labelled-by"]; len(got) != 2 || got[0] != target || got[1] != other {
		t.Fatalf("mutating diagnostic result rewrote source relation %v", got)
	}
	if _, ok := snapshot.Nodes[0].Relations["new-relation"]; ok {
		t.Fatal("mutating diagnostic added relation to provider snapshot")
	}
	if got := second[0].Relations["labelled-by"]; len(got) != 2 || got[0] != target || got[1] != other {
		t.Fatalf("mutating result corrupted independent diagnostic %v", got)
	}
}
