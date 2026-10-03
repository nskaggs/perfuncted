package accessibility

import "testing"

// The size-limited projection prunes each probe to fit the budget. A probe must
// own the nodes it prunes: pruning a shared Relations map deleted relations from
// the caller's snapshot and from every larger probe, so the search measured
// already-pruned graphs and returned a prefix that was smaller than the budget
// allowed, with the input left corrupted.
func TestSnapshotProjectionLeavesTheSourceRelationsIntact(t *testing.T) {
	nodes := []Node{
		{ID: NodeID{BusName: "b", ObjectPath: "/0", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/1", Generation: 1}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}},
			"controls":    {{BusName: "b", ObjectPath: "/0", Generation: 1}},
		}},
		{ID: NodeID{BusName: "b", ObjectPath: "/2", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/3", Generation: 1}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/2", Generation: 1}},
		}},
	}
	source := Snapshot{Nodes: nodes}

	// A budget large enough to keep everything still exercises the search, which
	// probes a shorter prefix before settling on the full one.
	if _, err := boundSnapshotResponse(source, 1<<20); err != nil {
		t.Fatalf("project: %v", err)
	}

	if len(nodes[1].Relations) != 2 {
		t.Fatalf("source node 1 relations = %v, want both preserved", nodes[1].Relations)
	}
	if _, ok := nodes[1].Relations["controls"]; !ok {
		t.Fatal("projection deleted a relation from the source snapshot")
	}
	if len(nodes[3].Relations) != 1 {
		t.Fatalf("source node 3 relations = %v, want its relation preserved", nodes[3].Relations)
	}
}

// The returned graph must be owned: a caller that mutates it cannot reach back
// into the snapshot it was given.
func TestProjectedSnapshotOwnsItsNodes(t *testing.T) {
	nodes := []Node{
		{ID: NodeID{BusName: "b", ObjectPath: "/0", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/1", Generation: 1},
			Attributes: map[string]string{"k": "v"},
			Relations:  map[string][]NodeID{"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}}}},
	}
	source := Snapshot{Nodes: nodes}

	got, err := boundSnapshotResponse(source, 1<<20)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("projected nodes = %d, want 2", len(got.Nodes))
	}
	got.Nodes[1].Relations["labelled-by"] = nil
	delete(got.Nodes[1].Attributes, "k")

	if len(nodes[1].Relations["labelled-by"]) != 1 {
		t.Fatal("mutating the projected graph changed the source relations")
	}
	if nodes[1].Attributes["k"] != "v" {
		t.Fatal("mutating the projected graph changed the source attributes")
	}
}
