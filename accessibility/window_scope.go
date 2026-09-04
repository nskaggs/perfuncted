package accessibility

import (
	"context"
	"fmt"
	"strings"
)

type windowCandidate struct {
	node  Node
	score int
}

// ResolveWindow correlates a managed window with a top-level AT-SPI frame or
// dialog. PID is only a narrowing signal; title, role, geometry, and active
// state decide the final candidate.
func (b *dbusBackend) ResolveWindow(ctx context.Context, target WindowTarget) (WindowScope, error) {
	if strings.TrimSpace(target.ID) == "" {
		return WindowScope{}, fmt.Errorf("%w: window identity is empty", ErrNotFound)
	}
	apps, err := b.Applications(ctx)
	if err != nil {
		return WindowScope{}, err
	}
	candidates := b.windowCandidates(ctx, target, apps)
	if len(candidates) == 0 {
		return WindowScope{WindowID: target.ID, Title: target.Title}, &MatchError{Operation: "window correlation", Err: ErrNotFound}
	}
	selected, bestCandidates, ambiguous := chooseWindowCandidate(candidates)
	if ambiguous {
		return WindowScope{WindowID: target.ID, Title: target.Title, Candidates: bestCandidates}, &MatchError{Operation: "window correlation", Err: ErrAmbiguous, Candidates: bestCandidates}
	}
	if selected == nil {
		return WindowScope{WindowID: target.ID, Title: target.Title, Candidates: bestCandidates}, &MatchError{Operation: "window correlation", Err: ErrNotFound, Candidates: bestCandidates}
	}
	evidence := []string{"managed window identity", "AT-SPI top-level role"}
	if target.PID != 0 {
		evidence = append(evidence, "process ownership")
	}
	if target.Title != "" {
		evidence = append(evidence, "window title")
	}
	if target.Bounds.Width > 0 && target.Bounds.Height > 0 {
		evidence = append(evidence, "screen geometry")
	}
	if target.Active {
		evidence = append(evidence, "active state")
	}
	return WindowScope{WindowID: target.ID, Title: target.Title, Root: selected.ID, Candidates: bestCandidates, Evidence: evidence}, nil
}

func (b *dbusBackend) windowCandidates(ctx context.Context, target WindowTarget, apps []Application) []windowCandidate {
	var candidates []windowCandidate
	for _, app := range apps {
		if !windowAppMatches(app, target) {
			continue
		}
		snapshot, err := b.Snapshot(ctx, app.ID, SnapshotOptions{MaxDepth: 8, MaxNodes: 2048})
		if err != nil {
			continue
		}
		for _, node := range snapshot.Nodes {
			if node.ID == app.ID || !isWindowRole(node.Role) {
				continue
			}
			if score := windowCandidateScore(node, target); score > 0 {
				candidates = append(candidates, windowCandidate{node: node, score: score})
			}
		}
	}
	return candidates
}

func windowAppMatches(app Application, target WindowTarget) bool {
	if target.PID != 0 && (app.PID == 0 || app.PID != target.PID) {
		return false
	}
	return target.AppID == "" || strings.Contains(strings.ToLower(app.Name+" "+app.Description), strings.ToLower(target.AppID))
}

func chooseWindowCandidate(candidates []windowCandidate) (*Node, []Candidate, bool) {
	best := candidates[0].score
	for _, candidate := range candidates[1:] {
		if candidate.score > best {
			best = candidate.score
		}
	}
	bestCandidates := make([]Candidate, 0, len(candidates))
	var selected *Node
	for _, candidate := range candidates {
		if candidate.score != best {
			continue
		}
		bestCandidates = append(bestCandidates, candidateFromNode(candidate.node))
		if selected != nil {
			return nil, bestCandidates, true
		}
		node := candidate.node
		selected = &node
	}
	return selected, bestCandidates, false
}

func isWindowRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "frame", "dialog", "window", "internal-frame":
		return true
	default:
		return false
	}
}

func windowCandidateScore(node Node, target WindowTarget) int {
	score := 1
	name := strings.ToLower(strings.TrimSpace(node.Name))
	title := strings.ToLower(strings.TrimSpace(target.Title))
	if title != "" {
		switch {
		case name == title:
			score += 100
		case strings.Contains(name, title):
			score += 45
		case strings.Contains(strings.ToLower(node.Description), title):
			score += 25
		default:
			return 0
		}
	}
	if target.Bounds.Width > 0 && target.Bounds.Height > 0 && node.HasBounds {
		if rectOverlap(node.Bounds, target.Bounds) == 0 {
			return 0
		}
		score += 30
	}
	if target.Active && (node.Focused || node.Showing) {
		score += 15
	}
	if node.Visible || node.Showing {
		score += 5
	}
	return score
}

func rectOverlap(a, b Rect) int {
	left := maxInt(a.X, b.X)
	top := maxInt(a.Y, b.Y)
	right := minInt(a.X+a.Width, b.X+b.Width)
	bottom := minInt(a.Y+a.Height, b.Y+b.Height)
	if right <= left || bottom <= top {
		return 0
	}
	return (right - left) * (bottom - top)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ WindowResolver = (*dbusBackend)(nil)
