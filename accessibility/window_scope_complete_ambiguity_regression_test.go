package accessibility

import (
	"testing"
)

// When multiple accessible frames have identical authoritative evidence,
// the ambiguity report must enumerate all co-best candidates. Omitting the
// later matches hides other potential targets and makes the diagnostic depend
// on enumeration order.
func TestWindowCandidateAmbiguityReportsEveryBestCandidate(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/one", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/low", Generation: 1}, Name: "Other"}, score: 20},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/two", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/three", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/four", Generation: 1}, Name: "Editor"}, score: 90},
	}
	selected, matches, ambiguous := chooseWindowCandidate(candidates)
	if selected != nil || !ambiguous || len(matches) != 4 {
		t.Fatalf("ambiguous selection: selected=%+v ambiguous=%v candidates=%+v, want all four co-best", selected, ambiguous, matches)
	}
	for i, path := range []string{"/one", "/two", "/three", "/four"} {
		if matches[i].ID.ObjectPath != path {
			t.Fatalf("candidate %d = %+v, want path %s", i, matches[i], path)
		}
	}
}
