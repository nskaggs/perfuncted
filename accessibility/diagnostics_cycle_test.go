package accessibility

import "testing"

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
