package accessibility

import (
	"reflect"
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

func TestCandidatesForQueryCyclicParentsAreBounded(t *testing.T) {
	buttonID := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 4}
	dialogID := NodeID{BusName: "org.test", ObjectPath: "/dialog", Generation: 4}
	snapshot := Snapshot{Nodes: []Node{
		{ID: buttonID, Name: "Save", Parent: dialogID},
		{ID: dialogID, Role: "dialog", Name: "Settings", Parent: buttonID},
	}}
	candidates := CandidatesForQuery(snapshot, Query{Name: "save"}, 4)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if got := candidates[0].Breadcrumb; len(got) != 1 || got[0] != "dialog: Settings" {
		t.Fatalf("cyclic breadcrumb = %v, want one parent label", got)
	}
}

func TestCandidatesForQuerySelfParentDoesNotRepeatCandidate(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 4}
	snapshot := Snapshot{Nodes: []Node{{ID: id, Parent: id, Name: "Save"}}}
	candidates := CandidatesForQuery(snapshot, Query{Name: "save"}, 4)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if got := candidates[0].Breadcrumb; len(got) != 0 {
		t.Fatalf("self-parent breadcrumb = %v, want none", got)
	}
}

// Diagnostic breadcrumbs must reflect only ancestors actually present in the
// same bounded snapshot. A vanished or incomplete parent chain must not be
// fabricated from a stale, out-of-snapshot accessibility identity.
func TestCandidateBreadcrumbStopsAtMissingParentWithoutFabricatingAncestors(t *testing.T) {
	app := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 5}
	dialog := NodeID{BusName: app.BusName, ObjectPath: "/dialog", Generation: 5}
	absent := NodeID{BusName: app.BusName, ObjectPath: "/unobserved", Generation: 5}
	button := NodeID{BusName: app.BusName, ObjectPath: "/save", Generation: 5}
	for _, tc := range []struct {
		name  string
		nodes []Node
		want  []string
	}{
		{"direct parent missing", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: button, Name: "Save", Role: "button", Parent: absent}}, nil},
		{"known parent followed by missing ancestor", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: dialog, Name: "Settings", Role: "dialog", Parent: absent}, {ID: button, Name: "Save", Role: "button", Parent: dialog}}, []string{"dialog: Settings"}},
		{"complete chain", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: dialog, Name: "Settings", Role: "dialog", Parent: app}, {ID: button, Name: "Save", Role: "button", Parent: dialog}}, []string{"application: Editor", "dialog: Settings"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := CandidatesForQuery(Snapshot{Nodes: tc.nodes}, Query{Name: "Save"}, 32)
			if len(candidates) != 1 || candidates[0].ID != button || !reflect.DeepEqual(candidates[0].Breadcrumb, tc.want) {
				t.Fatalf("candidates=%+v want breadcrumbs=%v", candidates, tc.want)
			}
		})
	}
}

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

func TestCandidatesForQueryStateOrAttributeOnlyStillReportsContext(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 4}
	snapshot := Snapshot{Nodes: []Node{{ID: id, Role: "button", Name: "Save"}}}
	queries := []struct {
		name  string
		query Query
	}{
		{name: "states", query: Query{States: []string{"focused"}}},
		{name: "attributes", query: Query{Attributes: map[string]string{"aria-label": "save"}}},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			candidates := CandidatesForQuery(snapshot, tc.query, 1)
			if len(candidates) != 1 || candidates[0].ID != id {
				t.Fatalf("candidates = %+v, want button diagnostic", candidates)
			}
		})
	}
}
