package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
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

func TestWindowCorrelationAcceptsTitleWithCompositorAppIDWithoutPID(t *testing.T) {
	target := WindowTarget{ID: "window-1", Title: "Browser Console", AppID: "org.mozilla.firefox"}
	if !windowTargetHasAuthoritativeEvidence(target) {
		t.Fatal("title plus compositor AppID was treated as unsupported correlation")
	}
	if windowTargetHasAuthoritativeEvidence(WindowTarget{ID: "window-1", AppID: "org.mozilla.firefox"}) {
		t.Fatal("AppID-only target unexpectedly gained authoritative correlation evidence")
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

func TestWindowCandidateScoreAcceptsAccessibleShortTitle(t *testing.T) {
	target := WindowTarget{Title: "Browser Console - Mozilla Firefox", Bounds: Rect{X: 10, Y: 20, Width: 300, Height: 200}}
	node := Node{Name: "Browser Console", Role: "frame", Bounds: target.Bounds, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score <= 0 {
		t.Fatalf("short accessible title score = %d, want positive", score)
	}
}

func TestReadWindowCandidateUsesCurrentCacheMetadata(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	child := NodeID{BusName: root.BusName, ObjectPath: "/console", Generation: root.Generation}
	backend := &dbusBackend{
		generation: root.Generation,
		cacheItems: make(map[NodeID]cacheItem),
		cacheApps:  map[string]bool{root.BusName: true},
	}
	backend.cacheItems[child] = cacheItem{
		Object:      cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
		Name:        "Parent process Browser Console",
		Description: "Firefox",
		Role:        23,
		States:      []uint32{(1 << 8) | (1 << 11) | (1 << 25) | (1 << 30)},
	}

	node, err := backend.readWindowCandidate(context.Background(), child, root)
	if err != nil {
		t.Fatalf("readWindowCandidate: %v", err)
	}
	if node.Name != "Parent process Browser Console" || node.Role != "frame" {
		t.Fatalf("cached candidate = %+v", node)
	}
	if !node.Enabled || !node.Showing || !node.Visible {
		t.Fatalf("cached candidate states = %+v", node.States)
	}
}

func TestRoleNameIncludesATSPIWindowRole(t *testing.T) {
	if got := roleName(69); got != "window" {
		t.Fatalf("AT-SPI role 69 = %q, want window", got)
	}
	if !isWindowRole(roleName(69)) {
		t.Fatalf("AT-SPI role 69 was not accepted as a top-level window")
	}
}

func TestFirefoxEmbeddedConsoleRoleIsAcceptedAsWindowScope(t *testing.T) {
	if got := roleName(78); got != "embedded" {
		t.Fatalf("AT-SPI role 78 = %q, want embedded", got)
	}
	if !isWindowRole("embedded") {
		t.Fatal("Firefox embedded Browser Console role was not accepted as a top-level window")
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

func TestWindowTitleMatchRejectsPrefixSubstring(t *testing.T) {
	// "Edit" is a prefix of "Editor - Preferences" but not a word-boundary
	// match; correlation must fail closed.
	if _, ok := windowTitleMatchScore("editor - preferences", "edit", ""); ok {
		t.Fatal("title substring prefix was accepted without word-boundary check")
	}
}

func TestWindowTitleMatchAcceptsWordBoundaryPrefix(t *testing.T) {
	// "Editor" occurs at a word boundary in "Editor - Preferences".
	score, ok := windowTitleMatchScore("editor", "editor - preferences", "")
	if !ok || score <= 0 {
		t.Fatalf("word-boundary prefix match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchAcceptsSeparatorDelimitedMatch(t *testing.T) {
	score, ok := windowTitleMatchScore("console", "browser console - mozilla firefox", "")
	if !ok || score <= 0 {
		t.Fatalf("separator-delimited match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchRejectsUnicodeFalseBoundary(t *testing.T) {
	// Unicode letters should be treated as word characters, so "edit" must
	// not match "Editorédité" (where "édité" continues the word).
	if _, ok := windowTitleMatchScore("editorédité", "edit", ""); ok {
		t.Fatal("unicode-adjacent substring was accepted as word boundary")
	}
	if _, ok := windowTitleMatchScore("editor", "éeditor - preferences", ""); ok {
		t.Fatal("substring following a Unicode letter was accepted as word boundary")
	}
}

func TestWindowTitleMatchAcceptsUnicodeExactMatch(t *testing.T) {
	score, ok := windowTitleMatchScore("éditeur", "éditeur", "")
	if !ok || score != 100 {
		t.Fatalf("unicode exact match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchUsesRuneCountsForMinimumLengths(t *testing.T) {
	tests := []struct {
		name        string
		windowName  string
		title       string
		description string
	}{
		{
			name:       "single-character title",
			windowName: "a - settings",
			title:      "a",
		},
		{
			name:       "short Unicode title",
			windowName: "éé - settings",
			title:      "éé",
		},
		{
			name:       "short Unicode window name",
			windowName: "éééé",
			title:      "éééé - browser",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := windowTitleMatchScore(tt.windowName, tt.title, tt.description); ok {
				t.Fatalf("short Unicode evidence matched: window=%q title=%q", tt.windowName, tt.title)
			}
		})
	}
}

func TestWindowTitleMatchDescriptionRequiresWordBoundary(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		wantScore   int
		wantOK      bool
	}{
		{
			name:        "rejects title prefix inside a word",
			title:       "edit",
			description: "editor preferences",
		},
		{
			name:        "accepts separator-delimited title",
			title:       "editor",
			description: "open editor settings",
			wantScore:   25,
			wantOK:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, ok := windowTitleMatchScore("preferences", tt.title, tt.description)
			if score != tt.wantScore || ok != tt.wantOK {
				t.Fatalf("description match: score=%d ok=%v, want score=%d ok=%v", score, ok, tt.wantScore, tt.wantOK)
			}
		})
	}
}

func TestWindowTitleMatchSameTitleSiblingsAmbiguous(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/a", Generation: 1}, Role: "frame", Name: "Editor"}, score: 100},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/b", Generation: 1}, Role: "frame", Name: "Editor"}, score: 100},
	}
	selected, _, ambiguous := chooseWindowCandidate(candidates)
	if !ambiguous || selected != nil {
		t.Fatalf("same-title siblings: selected=%+v ambiguous=%v", selected, ambiguous)
	}
}
