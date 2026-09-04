package accessibility

import (
	"errors"
	"testing"
)

func TestWindowCandidateCorrelationUsesIndependentEvidence(t *testing.T) {
	target := WindowTarget{ID: "window-2", Title: "Editor", PID: 44, Bounds: Rect{X: 100, Y: 100, Width: 400, Height: 300}, Active: true}
	match := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/window-2", Generation: 3}, Role: "frame", Name: "Editor", Bounds: target.Bounds, HasBounds: true, Showing: true, Visible: true}
	other := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/window-1", Generation: 3}, Role: "frame", Name: "Preferences", Bounds: Rect{X: 700, Y: 100, Width: 300, Height: 200}, HasBounds: true, Showing: true, Visible: true}
	if windowCandidateScore(match, target) <= windowCandidateScore(other, target) {
		t.Fatalf("title/geometry evidence did not prefer matching window: match=%d other=%d", windowCandidateScore(match, target), windowCandidateScore(other, target))
	}
	if windowCandidateScore(other, target) != 0 {
		t.Fatalf("non-overlapping/title-mismatched window scored as a match: %d", windowCandidateScore(other, target))
	}
}

func TestWindowCorrelationWithIDOnlyRefusesToGuess(t *testing.T) {
	target := WindowTarget{ID: "window-1"}
	first := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/one", Generation: 1}, Role: "frame", Name: "One", Visible: true, Showing: true}
	second := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/two", Generation: 1}, Role: "dialog", Name: "Two", Visible: true, Showing: true}
	if windowCandidateScore(first, target) != windowCandidateScore(second, target) {
		t.Fatal("ID-only target unexpectedly gained evidence from unrelated metadata")
	}
	if windowCandidateScore(first, target) == 0 {
		t.Fatal("visible top-level candidate was discarded before ambiguity handling")
	}
	if !errors.Is((&MatchError{Err: ErrAmbiguous}), ErrAmbiguous) {
		t.Fatal("MatchError does not preserve ambiguity category")
	}
}

func TestChooseWindowCandidateRefusesSameProcessAmbiguity(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 1}, Role: "frame", Name: "Editor"}, score: 16},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/dialog", Generation: 1}, Role: "dialog", Name: "Editor"}, score: 16},
	}
	selected, context, ambiguous := chooseWindowCandidate(candidates)
	if selected != nil || !ambiguous || len(context) != 2 {
		t.Fatalf("same-process candidates selected=%+v context=%+v ambiguous=%v", selected, context, ambiguous)
	}
}

func TestWindowAppMatchingUsesPIDBeforeCompositorAppIDSpelling(t *testing.T) {
	app := Application{Node: Node{Name: "zenity"}, PID: 42}
	if !windowAppMatches(app, WindowTarget{PID: 42, AppID: "org.gnome.Zenity"}) {
		t.Fatal("PID-correlated application rejected due to compositor/AT-SPI app-id spelling")
	}
	if windowAppMatches(app, WindowTarget{AppID: "org.gnome.Zenity"}) {
		t.Fatal("app-id-only target matched unrelated application")
	}
}

func TestWindowCandidateScoreRejectsChangedTitleAndGeometry(t *testing.T) {
	target := WindowTarget{Title: "Editor", Bounds: Rect{X: 10, Y: 20, Width: 300, Height: 200}}
	node := Node{Name: "Editor", Role: "frame", Bounds: target.Bounds, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score <= 0 {
		t.Fatalf("matching candidate score = %d, want positive", score)
	}

	changedTitle := node
	changedTitle.Name = "Preferences"
	if score := windowCandidateScore(changedTitle, target); score != 0 {
		t.Fatalf("changed-title candidate score = %d, want zero", score)
	}

	changedGeometry := node
	changedGeometry.Bounds.X += target.Bounds.Width + 1
	if score := windowCandidateScore(changedGeometry, target); score != 0 {
		t.Fatalf("non-overlapping candidate score = %d, want zero", score)
	}
}

func TestChooseWindowCandidateSelectsUniqueStrongestEvidence(t *testing.T) {
	weak := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/weak", Generation: 1}, Role: "frame", Name: "Editor"}
	strong := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/strong", Generation: 1}, Role: "frame", Name: "Editor"}
	selected, candidates, ambiguous := chooseWindowCandidate([]windowCandidate{
		{node: weak, score: 4},
		{node: strong, score: 12},
	})
	if ambiguous || selected == nil || selected.ID != strong.ID {
		t.Fatalf("selected=%+v candidates=%+v ambiguous=%v", selected, candidates, ambiguous)
	}
	if len(candidates) != 1 || candidates[0].ID != strong.ID {
		t.Fatalf("best candidates = %+v, want strongest candidate only", candidates)
	}
}
