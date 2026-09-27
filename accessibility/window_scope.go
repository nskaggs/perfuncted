package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// windowCandidateReadTimeout is the safety fallback for one identity read
// when direct callers provide no deadline.
const windowCandidateReadTimeout = 750 * time.Millisecond

func windowCandidateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return contextutil.WithTimeoutFallback(ctx, windowCandidateReadTimeout)
}

type windowCandidate struct {
	node  Node
	app   Application
	score int
}

// ResolveWindow correlates a managed window with a top-level AT-SPI frame or
// dialog. PID is only a narrowing signal; title, role, geometry, and active
// state decide the final candidate.
func (b *dbusBackend) ResolveWindow(ctx context.Context, target WindowTarget) (WindowScope, error) { //nolint:gocyclo // correlation keeps each ambiguity and evidence rule explicit.
	return b.resolveWindow(ctx, target, false)
}

// ResolveWindowFresh performs the same correlation from current AT-SPI
// children and identity properties. Incomplete provider reads fail closed so
// a locator cannot inherit a stale or partial scope root.
func (b *dbusBackend) ResolveWindowFresh(ctx context.Context, target WindowTarget) (WindowScope, error) {
	for attempt := 0; attempt < 2; attempt++ {
		scope, err := b.resolveWindow(ctx, target, true)
		if !errors.Is(err, ErrStaleGeneration) || attempt == 1 {
			return scope, err
		}
	}
	return WindowScope{}, ErrStaleGeneration
}

func (b *dbusBackend) resolveWindow(ctx context.Context, target WindowTarget, fresh bool) (WindowScope, error) { //nolint:gocyclo // correlation keeps each ambiguity and evidence rule explicit.
	expectedGeneration := b.Generation()
	if strings.TrimSpace(target.ID) == "" {
		return WindowScope{}, fmt.Errorf("%w: window identity is empty", ErrNotFound)
	}
	if !windowTargetHasAuthoritativeEvidence(target) {
		return WindowScope{WindowID: target.ID, Title: target.Title}, &MatchError{Operation: "window correlation", Err: ErrUnsupportedCorrelation}
	}
	// Window correlation only needs application ownership and direct
	// top-level children. Applications() intentionally reads a rich node
	// snapshot, including optional Text/Component/Document interfaces; that
	// can block on Firefox while its chrome is publishing. Keep this resolver
	// on the narrow identity path so BrowserConsole startup is not held hostage
	// by unrelated application content.
	var apps []Application
	var err error
	if fresh {
		apps, err = b.windowApplicationsFresh(ctx, target)
	} else {
		apps, err = b.windowApplications(ctx, target)
	}
	if err != nil {
		return WindowScope{}, err
	}
	var candidates []windowCandidate
	if fresh {
		candidates, err = b.windowCandidatesFresh(ctx, target, apps)
		if err != nil {
			return WindowScope{}, err
		}
		if err := b.generationError(expectedGeneration); err != nil {
			return WindowScope{}, err
		}
	} else {
		candidates = b.windowCandidates(ctx, target, apps)
	}
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
	evidence := []string{"managed window identity", "AT-SPI top-level role", "application-root ownership"}
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
	var selectedApp Application
	for _, candidate := range candidates {
		if candidate.node.ID == selected.ID {
			selectedApp = candidate.app
			break
		}
	}
	return WindowScope{
		WindowID:        target.ID,
		Title:           target.Title,
		Root:            selected.ID,
		ApplicationRoot: selectedApp.ID,
		Application:     selectedApp,
		PID:             selectedApp.PID,
		AppID:           target.AppID,
		Generation:      selected.ID.Generation,
		Candidates:      bestCandidates,
		Evidence:        evidence,
	}, nil
}

func (b *dbusBackend) windowApplicationsFresh(ctx context.Context, target WindowTarget) ([]Application, error) { //nolint:gocyclo // app ownership and generation checks jointly establish a complete window scope.
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	expected := b.Generation()
	refs, err := b.childrenFresh(ctx, NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: expected})
	if err != nil {
		return nil, err
	}
	if len(refs) > maxApplications {
		return nil, fmt.Errorf("%w: application list exceeds %d entries", ErrIncompleteSnapshot, maxApplications)
	}
	wantPID := target.PID
	if wantPID == 0 {
		wantPID = target.PIDHint
	}
	apps := make([]Application, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, pidErr := b.connectionPID(ctx, ref.BusName)
		if wantPID != 0 {
			if pidErr != nil {
				return nil, fmt.Errorf("accessibility: resolve window application PID: %w", pidErr)
			}
			if pid != wantPID {
				continue
			}
		}
		id := b.refID(ref)
		app := Application{Node: Node{ID: id}, PID: pid}
		if wantPID == 0 && target.Title == "" && target.AppID != "" {
			if err := b.property(ctx, id, accessibleIface, "Name", &app.Name); err != nil {
				return nil, fmt.Errorf("accessibility: read window application name: %w", err)
			}
			if err := b.property(ctx, id, accessibleIface, "Description", &app.Description); err != nil {
				return nil, fmt.Errorf("accessibility: read window application description: %w", err)
			}
		}
		apps = append(apps, app)
	}
	if err := b.generationError(expected); err != nil {
		return nil, err
	}
	return apps, nil
}

func (b *dbusBackend) windowCandidatesFresh(ctx context.Context, target WindowTarget, apps []Application) ([]windowCandidate, error) {
	expected := b.Generation()
	var candidates []windowCandidate
	for _, app := range apps {
		if app.ID.Generation != expected {
			return nil, ErrStaleGeneration
		}
		if err := b.generationError(expected); err != nil {
			return nil, err
		}
		if !windowAppMatches(app, target) {
			continue
		}
		children, err := b.childrenFresh(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		if len(children) > maxApplications {
			return nil, fmt.Errorf("%w: window child list exceeds %d entries", ErrIncompleteSnapshot, maxApplications)
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := b.refID(child)
			readCtx, cancel := windowCandidateContext(ctx)
			node, readErr := b.readWindowCandidateFresh(readCtx, id, app.ID, target.Title != "")
			cancel()
			if readErr != nil {
				return nil, fmt.Errorf("accessibility: resolve fresh window candidate %s: %w", id.ObjectPath, readErr)
			}
			if !isWindowRole(node.Role) {
				continue
			}
			if score := windowCandidateScore(node, target); score > 0 {
				candidates = append(candidates, windowCandidate{node: node, app: app, score: score})
			}
		}
	}
	return candidates, b.generationError(expected)
}

func (b *dbusBackend) readWindowCandidateFresh(ctx context.Context, id, parent NodeID, needDescription bool) (Node, error) {
	if _, err := b.object(id); err != nil {
		return Node{}, err
	}
	node := Node{ID: id, Parent: parent}
	if err := b.property(ctx, id, accessibleIface, "Name", &node.Name); err != nil {
		return Node{}, err
	}
	if needDescription {
		if err := b.property(ctx, id, accessibleIface, "Description", &node.Description); err != nil {
			return Node{}, err
		}
	}
	var role uint32
	if err := b.call(ctx, id, accessibleIface+".GetRole", nil, &role); err != nil {
		return Node{}, err
	}
	node.RoleID = role
	node.Role = roleName(role)
	var states []uint32
	if err := b.call(ctx, id, accessibleIface+".GetState", nil, &states); err != nil {
		return Node{}, err
	}
	b.applyStates(states, &node)
	return node, b.generationError(id.Generation)
}

func (b *dbusBackend) windowApplications(ctx context.Context, target WindowTarget) ([]Application, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	refs, err := b.children(ctx, b.desktop())
	if err != nil {
		return nil, err
	}
	apps := make([]Application, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, pidErr := b.connectionPID(ctx, ref.BusName)
		if target.PID != 0 && (pidErr != nil || pid == 0 || pid != target.PID) {
			continue
		}
		id := b.refID(ref)
		app := Application{Node: Node{ID: id}, PID: pid}
		// PID is the normal authoritative discriminator. Read the small
		// identity fields only for callers that cannot supply one, such as
		// app-id/title based diagnostic resolution.
		if target.PID == 0 {
			_ = b.property(ctx, id, accessibleIface, "Name", &app.Name)
			_ = b.property(ctx, id, accessibleIface, "Description", &app.Description)
		}
		apps = append(apps, app)
	}
	return apps, nil
}

func windowTargetHasAuthoritativeEvidence(target WindowTarget) bool {
	if target.PID != 0 || target.PIDHint != 0 || target.Active || target.Focused {
		return true
	}
	if strings.TrimSpace(target.Title) != "" {
		return true
	}
	return target.Bounds.Width > 0 && target.Bounds.Height > 0
}

func (b *dbusBackend) windowCandidates(ctx context.Context, target WindowTarget, apps []Application) []windowCandidate {
	var candidates []windowCandidate
	for _, app := range apps {
		if !windowAppMatches(app, target) {
			continue
		}
		// Window correlation only needs the application's immediate accessible
		// children: AT-SPI exposes top-level Firefox frames/dialogs there. The
		// generic application snapshot also loads the provider cache and walks
		// descendants, which can block on a Browser Console while we are still
		// trying to identify the window. Read only the direct children and their
		// small identity envelope; deeper inspection starts after correlation.
		children, err := b.children(ctx, app.ID)
		if err != nil {
			continue
		}
		for _, child := range children {
			id := b.refID(child)
			readCtx, cancel := windowCandidateContext(ctx)
			node, readErr := b.readWindowCandidate(readCtx, id, app.ID)
			cancel()
			if readErr != nil || !isWindowRole(node.Role) {
				continue
			}
			if score := windowCandidateScore(node, target); score > 0 {
				candidates = append(candidates, windowCandidate{node: node, app: app, score: score})
			}
		}
	}
	return candidates
}

func (b *dbusBackend) readWindowCandidate(ctx context.Context, id, parent NodeID) (Node, error) {
	node := Node{ID: id, Parent: parent}
	if item, ok := b.cachedItem(id); ok {
		node.Name = item.Name
		node.Description = item.Description
		node.RoleID = item.Role
		node.Role = roleName(item.Role)
		b.applyStates(item.States, &node)
		return node, nil
	}
	if err := b.property(ctx, id, accessibleIface, "Name", &node.Name); err != nil {
		return Node{}, err
	}
	_ = b.property(ctx, id, accessibleIface, "Description", &node.Description)
	var role uint32
	if err := b.call(ctx, id, accessibleIface+".GetRole", nil, &role); err != nil {
		return Node{}, err
	}
	node.RoleID = role
	node.Role = roleName(role)
	var states []uint32
	if err := b.call(ctx, id, accessibleIface+".GetState", nil, &states); err == nil {
		b.applyStates(states, &node)
	}
	return node, nil
}

func windowAppMatches(app Application, target WindowTarget) bool {
	pid := target.PID
	if pid == 0 {
		pid = target.PIDHint
	}
	if pid != 0 && (app.PID == 0 || app.PID != pid) {
		return false
	}
	// Compositor AppID and AT-SPI application names are not guaranteed to
	// share a spelling. Use AppID as a narrowing criterion only when it is the
	// sole caller-provided discriminator; PID/title/geometry remain authoritative
	// when available.
	if target.PID == 0 && target.Title == "" && target.AppID != "" {
		return strings.Contains(strings.ToLower(app.Name+" "+app.Description), strings.ToLower(target.AppID))
	}
	return true
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
	case "frame", "dialog", "window", "internal-frame", "embedded":
		// Firefox exposes its privileged Browser Console as an embedded
		// top-level child of the Firefox AT-SPI application. It still carries
		// the managed window's title, PID, and geometry, so accepting this role
		// here preserves authoritative correlation without broad desktop-root
		// guessing.
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
		titleScore, ok := windowTitleMatchScore(name, title, node.Description)
		if !ok {
			return 0
		}
		score += titleScore
	}
	if target.Bounds.Width > 0 && target.Bounds.Height > 0 && node.HasBounds {
		if rectOverlap(node.Bounds, target.Bounds) == 0 {
			return 0
		}
		score += 30
	}
	if (target.Active || target.Focused) && (node.Focused || node.Showing) {
		score += 15
	}
	if node.Visible || node.Showing {
		score += 5
	}
	return score
}

func windowTitleMatchScore(name, title, description string) (int, bool) {
	switch {
	case name == title:
		return 100, true
	case len([]rune(title)) >= 3 && strings.Contains(name, title) && hasWordBoundaryMatch(name, title):
		return 45, true
	case len([]rune(name)) >= 5 && strings.Contains(title, name) && hasWordBoundaryMatch(title, name):
		// Firefox chrome windows may append the browser or profile title in
		// the compositor while AT-SPI exposes the stable dialog/frame name.
		// Require a word-boundary match so generic short names cannot
		// correlate with unrelated longer titles.
		return 35, true
	case len([]rune(title)) >= 3 && strings.Contains(strings.ToLower(description), title) && hasWordBoundaryMatch(strings.ToLower(description), title):
		return 25, true
	default:
		return 0, false
	}
}

// hasWordBoundaryMatch reports whether name occurs in title at a word
// boundary. Both inputs are already lower-cased and trimmed by the caller.
func hasWordBoundaryMatch(title, name string) bool {
	nameRunes := []rune(name)
	titleRunes := []rune(title)
	for i := 0; i+len(nameRunes) <= len(titleRunes); i++ {
		match := true
		for j, r := range nameRunes {
			if titleRunes[i+j] != r {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		beforeOK := i == 0 || !isWordRune(titleRunes[i-1])
		after := i + len(nameRunes)
		afterOK := after >= len(titleRunes) || !isWordRune(titleRunes[after])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func rectOverlap(a, b Rect) int {
	left := max(a.X, b.X)
	top := max(a.Y, b.Y)
	right := min(a.X+a.Width, b.X+b.Width)
	bottom := min(a.Y+a.Height, b.Y+b.Height)
	if right <= left || bottom <= top {
		return 0
	}
	return (right - left) * (bottom - top)
}

var _ WindowResolver = (*dbusBackend)(nil)
