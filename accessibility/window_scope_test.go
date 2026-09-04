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
