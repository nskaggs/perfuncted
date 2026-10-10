package accessibility

import (
	"strings"
	"testing"
)

// Prefix probes may prune references to nodes outside the prefix, while the
// source snapshot retains its original children and relations.
func TestSnapshotProjectionLeavesTheSourceRelationsIntact(t *testing.T) {
	nodes := []Node{
		{ID: NodeID{BusName: "b", ObjectPath: "/0", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/1", Generation: 1}, Attributes: map[string]string{"label": "owned"}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}},
			"controls":    {{BusName: "b", ObjectPath: "/0", Generation: 1}},
		}},
		{ID: NodeID{BusName: "b", ObjectPath: "/2", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/3", Generation: 1}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/2", Generation: 1}},
		}},
	}
	nodes[0].Children = []NodeID{{BusName: "b", ObjectPath: "/3", Generation: 1}}
	nodes[1].Relations["flows-to"] = []NodeID{{BusName: "b", ObjectPath: "/3", Generation: 1}}
	source := Snapshot{Root: nodes[0], Nodes: nodes}

	fullSize := snapshotJSONSize(source)
	projected, err := boundSnapshotResponse(source, fullSize-1)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(projected.Nodes) != len(nodes)-1 {
		t.Fatalf("projected node count = %d, want %d", len(projected.Nodes), len(nodes)-1)
	}
	if len(projected.Nodes[0].Children) != 0 {
		t.Fatalf("projected root children = %v, want references outside the prefix pruned", projected.Nodes[0].Children)
	}
	if _, ok := projected.Nodes[1].Relations["flows-to"]; ok {
		t.Fatal("projected relation retained a target outside the prefix")
	}
	projected.Nodes[1].Attributes["label"] = "changed"
	if nodes[1].Attributes["label"] != "owned" {
		t.Fatal("mutating a truncated projection changed the source attributes")
	}

	if len(nodes[1].Relations) != 3 {
		t.Fatalf("source node 1 relations = %v, want all three preserved", nodes[1].Relations)
	}
	if _, ok := nodes[1].Relations["controls"]; !ok {
		t.Fatal("projection deleted a relation from the source snapshot")
	}
	if len(nodes[3].Relations) != 1 {
		t.Fatalf("source node 3 relations = %v, want its relation preserved", nodes[3].Relations)
	}
	if len(nodes[0].Children) != 1 || nodes[0].Children[0].ObjectPath != "/3" {
		t.Fatalf("source root children = %v, want the original reference preserved", nodes[0].Children)
	}
	if len(nodes[1].Relations["flows-to"]) != 1 || nodes[1].Relations["flows-to"][0].ObjectPath != "/3" {
		t.Fatalf("source node 1 flows-to = %v, want the original reference preserved", nodes[1].Relations["flows-to"])
	}
}

// The returned graph must be owned: a caller that mutates it cannot reach back
// into the snapshot it was given.
func TestProjectedSnapshotOwnsItsNodes(t *testing.T) {
	nodes := []Node{
		{
			ID:         NodeID{BusName: "b", ObjectPath: "/0", Generation: 1},
			Interfaces: []string{"root-interface"},
			States:     []string{"root-state"},
			Children:   []NodeID{{BusName: "b", ObjectPath: "/1", Generation: 1}},
			Relations:  map[string][]NodeID{"owns": {{BusName: "b", ObjectPath: "/1", Generation: 1}}},
			Actions:    []Action{{Name: "root-action"}},
			Warnings:   []string{"root-warning"},
		},
		{
			ID:         NodeID{BusName: "b", ObjectPath: "/1", Generation: 1},
			Interfaces: []string{"org.test.Interface"},
			Attributes: map[string]string{"k": "v"},
			States:     []string{"visible"},
			Children:   []NodeID{{BusName: "b", ObjectPath: "/0", Generation: 1}},
			Relations:  map[string][]NodeID{"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}}},
			Value:      &ValueInfo{Current: 1},
			Actions:    []Action{{Name: "activate"}},
			Selection:  &SelectionInfo{SelectedChildCount: 1},
			Table:      &TableInfo{Rows: 2, Columns: 3},
			Document:   &DocumentInfo{Locale: "en"},
			Warnings:   []string{"warning"},
		},
	}
	source := Snapshot{Nodes: nodes}

	got, err := boundSnapshotResponse(source, 1<<20)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("projected nodes = %d, want 2", len(got.Nodes))
	}
	mutateProjectedNodeForOwnershipCheck(&got.Nodes[0])
	assertProjectedNeighborUnchanged(t, got.Nodes[1])
	mutateProjectedNodeForOwnershipCheck(&got.Nodes[1])
	assertSourceNodeUnchanged(t, nodes[1])
}

func mutateProjectedNodeForOwnershipCheck(node *Node) {
	node.Interfaces = append(node.Interfaces, "next-interface")
	node.States = append(node.States, "next-state")
	node.Children = append(node.Children, NodeID{BusName: "b", ObjectPath: "/extra", Generation: 1})
	for relation := range node.Relations {
		node.Relations[relation] = append(node.Relations[relation], NodeID{BusName: "b", ObjectPath: "/extra", Generation: 1})
		break
	}
	node.Actions = append(node.Actions, Action{Name: "next-action"})
	node.Warnings = append(node.Warnings, "next-warning")
	if node.Attributes != nil {
		delete(node.Attributes, "k")
	}
	if len(node.Interfaces) > 0 {
		node.Interfaces[0] = "changed"
	}
	if len(node.States) > 0 {
		node.States[0] = "changed"
	}
	if len(node.Children) > 0 {
		node.Children[0].ObjectPath = "/changed"
	}
	if node.Value != nil {
		node.Value.Current = 2
	}
	if len(node.Actions) > 0 {
		node.Actions[0].Name = "changed"
	}
	if node.Selection != nil {
		node.Selection.SelectedChildCount = 2
	}
	if node.Table != nil {
		node.Table.Rows = 4
	}
	if node.Document != nil {
		node.Document.Locale = "changed"
	}
	if len(node.Warnings) > 0 {
		node.Warnings[0] = "changed"
	}
}

func assertProjectedNeighborUnchanged(t *testing.T, node Node) {
	t.Helper()
	if node.Interfaces[0] != "org.test.Interface" || node.States[0] != "visible" ||
		node.Children[0].ObjectPath != "/0" || node.Relations["labelled-by"][0].ObjectPath != "/0" ||
		node.Actions[0].Name != "activate" || node.Warnings[0] != "warning" {
		t.Fatal("appending to one projected node changed a neighboring node")
	}
}

func assertSourceNodeUnchanged(t *testing.T, node Node) {
	t.Helper()
	if len(node.Relations["labelled-by"]) != 1 {
		t.Fatal("mutating the projected graph changed the source relations")
	}
	if node.Attributes["k"] != "v" {
		t.Fatal("mutating the projected graph changed the source attributes")
	}
	if node.Interfaces[0] != "org.test.Interface" || node.States[0] != "visible" || node.Children[0].ObjectPath != "/0" {
		t.Fatal("mutating the projected node changed source collections")
	}
	if node.Value.Current != 1 || node.Actions[0].Name != "activate" || node.Selection.SelectedChildCount != 1 {
		t.Fatal("mutating the projected node changed source value, actions, or selection")
	}
	if node.Table.Rows != 2 || node.Document.Locale != "en" || node.Warnings[0] != "warning" {
		t.Fatal("mutating the projected node changed source table, document, or warnings")
	}
}

func TestBoundSnapshotResponseDoesNotMutateNodesItTrims(t *testing.T) {
	text := "value " + strings.Repeat("long text ", 400)
	source := Snapshot{
		Root: Node{ID: NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}},
		Nodes: []Node{{
			ID:   NodeID{BusName: "b", ObjectPath: "/root", Generation: 1},
			Text: text, Attributes: map[string]string{"label": "source"},
		}},
	}
	projected, err := boundSnapshotResponse(source, 1024)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(projected.Nodes) != 0 || projected.Root.ID != source.Root.ID {
		t.Fatalf("projected minimal response = %+v, want the original root identity without oversized nodes", projected)
	}
	if source.Nodes[0].Text != text || source.Nodes[0].TextTruncated || source.Nodes[0].Attributes["label"] != "source" {
		t.Fatal("trimming the projection changed the source node")
	}
}

func TestProjectedSnapshotOwnsRootWithoutNodes(t *testing.T) {
	source := Snapshot{Root: Node{
		ID:         NodeID{BusName: "b", ObjectPath: "/root", Generation: 1},
		Attributes: map[string]string{"label": "owned"},
	}}

	projected, err := boundSnapshotResponse(source, 1<<20)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	projected.Root.Attributes["label"] = "changed"
	if source.Root.Attributes["label"] != "owned" {
		t.Fatal("mutating a projection without nodes changed the source root")
	}
}
