package accessibility

import (
	"strings"
	"testing"
)

// Providers can publish shared nodes and an oversized or incomplete graph.
// The semantic view must remain bounded without silently losing evidence
// that it is incomplete, even if a shared child appears under two parents.
func TestBuildOutlineBoundsDepthSizeAndSharedChildReferences(t *testing.T) {
	root := NodeID{BusName: "org.example", ObjectPath: "/root", Generation: 7}
	a := NodeID{BusName: "org.example", ObjectPath: "/a", Generation: 7}
	b := NodeID{BusName: "org.example", ObjectPath: "/b", Generation: 7}
	leaf := NodeID{BusName: "org.example", ObjectPath: "/leaf", Generation: 7}
	snap := Snapshot{Generation: 7, Root: Node{ID: root}, Nodes: []Node{
		{ID: root, Name: "Root", Children: []NodeID{a, b}},
		{ID: a, Name: "A", Children: []NodeID{leaf}},
		{ID: b, Name: "B", Children: []NodeID{leaf}},
		{ID: leaf, Name: "Shared leaf"},
	}}
	depth := BuildOutline(snap, OutlineOptions{MaxDepth: 1, MaxNodes: 16})
	if !depth.Truncated || len(depth.Root.Children) != 2 || len(depth.Root.Children[0].Children) != 0 || len(depth.Root.Children[1].Children) != 0 {
		t.Fatalf("depth bound not enforced: %+v", depth)
	}
	size := BuildOutline(snap, OutlineOptions{MaxNodes: 2, MaxDepth: 10})
	if !size.Truncated || len(size.Root.Children) != 1 || size.Root.Children[0].Name != "A" || len(size.Root.Children[0].Children) != 0 {
		t.Fatalf("node limit not enforced: %+v", size)
	}
	complete := BuildOutline(snap, OutlineOptions{MaxDepth: 8, MaxNodes: 16})
	if complete.Truncated || len(complete.Root.Children) != 2 || len(complete.Root.Children[0].Children) != 1 || len(complete.Root.Children[1].Children) != 1 {
		t.Fatalf("legitimate shared child was lost or marked cyclic: %+v", complete)
	}
	absent := BuildOutline(Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: a}}}, OutlineOptions{})
	if !absent.Truncated {
		t.Fatal("missing snapshot root claimed complete outline")
	}
}

func TestSemanticCandidateBreadcrumbsAreRootFirstBoundedAndIndependent(t *testing.T) {
	root := NodeID{BusName: "org.example", ObjectPath: "/root", Generation: 8}
	mid := NodeID{BusName: "org.example", ObjectPath: "/mid", Generation: 8}
	leaf := NodeID{BusName: "org.example", ObjectPath: "/leaf", Generation: 8}
	snap := Snapshot{Generation: 8, Nodes: []Node{
		{ID: root, Role: "application", Name: "Host"},
		{ID: mid, Role: "dialog", Name: "Preferences", Parent: root},
		{ID: leaf, Role: "button", Name: "Apply", Parent: mid},
	}}
	found := CandidatesForQuery(snap, Query{Name: "apply"}, 0)
	if len(found) != 1 || found[0].ID != leaf || strings.Join(found[0].Breadcrumb, " / ") != "application: Host / dialog: Preferences" {
		t.Fatalf("breadcrumb failed root-first correlation: %+v", found)
	}
	limited := CandidatesForQuery(snap, Query{}, 1)
	if len(limited) != 1 {
		t.Fatalf("candidate limit not enforced: %+v", limited)
	}
	invalidLimit := CandidatesForQuery(snap, Query{}, 129)
	if len(invalidLimit) != 3 {
		t.Fatalf("excessive candidate limit should normalize to bounded default: %+v", invalidLimit)
	}
}
