package accessibility

import "testing"

func TestBuildOutlineRejectsCyclicSnapshotEdges(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3}
	child := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: 3}
	snapshot := Snapshot{
		Root: Node{ID: root},
		Nodes: []Node{
			{ID: root, Role: "frame", Children: []NodeID{child}},
			{ID: child, Role: "button", Children: []NodeID{root}},
		},
	}
	outline := BuildOutline(snapshot, OutlineOptions{MaxDepth: 10, MaxNodes: 50})
	if !outline.Truncated {
		t.Fatal("cyclic provider child reference was accepted as a complete outline")
	}
	if len(outline.Root.Children) != 1 {
		t.Fatalf("valid child lost: %+v", outline.Root.Children)
	}
	if len(outline.Root.Children[0].Children) != 0 {
		t.Fatalf("cycle repeated an ancestor in outline: %+v", outline.Root.Children[0].Children)
	}
}

func TestBuildOutlineMissingChildMarksIncomplete(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3}
	missing := NodeID{BusName: "org.test", ObjectPath: "/missing", Generation: 3}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root, Children: []NodeID{missing}}}}
	outline := BuildOutline(snapshot, OutlineOptions{})
	if !outline.Truncated || len(outline.Root.Children) != 0 {
		t.Fatalf("missing node was reported complete: %+v", outline)
	}
}
