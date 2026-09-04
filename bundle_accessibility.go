package perfuncted

import (
	"context"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/util"
)

var openAccessibility = accessibility.OpenRuntime

// AccessibilityBundle exposes AT-SPI queries and typed automation for the active session.
// It is optional because many headless or minimal desktop sessions do not run
// an accessibility bus.
type AccessibilityBundle struct {
	backend accessibility.Backend
	bundleBase
}

// NewAccessibilityBundle binds a deterministic accessibility backend. It is
// useful for adapters and tests that need to exercise semantic workflows
// without a live AT-SPI bus.
func NewAccessibilityBundle(backend accessibility.Backend) *AccessibilityBundle {
	return &AccessibilityBundle{backend: backend, bundleBase: bundleBase{capability: CapabilityAccessibility}}
}

func (b *AccessibilityBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.bundleBase.checkAvailable(operation, !util.IsNil(b.backend))
}

func (b *AccessibilityBundle) close() error {
	if b == nil || util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

// Applications lists application roots currently registered with AT-SPI.
func (b *AccessibilityBundle) Applications(ctx context.Context) ([]accessibility.Application, error) {
	if err := b.checkAvailable("applications"); err != nil {
		return nil, err
	}
	apps, err := b.backend.Applications(ctx)
	return apps, b.operationError("applications", err)
}

// Snapshot returns a bounded accessibility tree. A zero root selects the
// virtual desktop root.
func (b *AccessibilityBundle) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	if err := b.checkAvailable("snapshot"); err != nil {
		return accessibility.Snapshot{}, err
	}
	snapshot, err := b.backend.Snapshot(ctx, root, opts)
	return snapshot, b.operationError("snapshot", err)
}

// Outline returns a bounded compact semantic view derived from a scoped
// accessibility snapshot.
func (b *AccessibilityBundle) Outline(ctx context.Context, root accessibility.NodeID, snapshotOptions accessibility.SnapshotOptions, outlineOptions accessibility.OutlineOptions) (accessibility.Outline, error) {
	if err := b.checkAvailable("outline"); err != nil {
		return accessibility.Outline{}, err
	}
	if source, ok := b.backend.(interface {
		Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error)
	}); ok {
		outline, err := source.Outline(ctx, root, snapshotOptions, outlineOptions)
		return outline, b.operationError("outline", err)
	}
	snapshot, err := b.backend.Snapshot(ctx, root, snapshotOptions)
	if err != nil {
		return accessibility.Outline{}, b.operationError("outline", err)
	}
	return accessibility.BuildOutline(snapshot, outlineOptions), nil
}

// AccessibilityWindow resolves a managed compositor window to its exact
// AT-SPI top-level subtree. It never substitutes the application root.
func (b *AccessibilityBundle) AccessibilityWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	if err := b.checkAvailable("window-root"); err != nil {
		return accessibility.WindowScope{}, err
	}
	resolver, ok := b.backend.(accessibility.WindowResolver)
	if !ok {
		return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrUnsupported)
	}
	scope, err := resolver.ResolveWindow(ctx, target)
	return scope, b.operationError("window-root", err)
}

// WindowRoot resolves a managed window by its authoritative native ID.
func (b *AccessibilityBundle) WindowRoot(ctx context.Context, windowID string) (accessibility.WindowScope, error) {
	if b == nil {
		return accessibility.WindowScope{}, ErrNilSession
	}
	if b.session == nil || b.session.Windows == nil {
		return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrUnsupported)
	}
	windows, err := b.session.Windows.List(ctx, WindowMatch{})
	if err != nil {
		return accessibility.WindowScope{}, b.operationError("window-root", err)
	}
	var selected *Window
	for _, candidate := range windows {
		if candidate.ID().String() != strings.TrimSpace(windowID) {
			continue
		}
		if selected != nil {
			return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrAmbiguous)
		}
		selected = candidate
	}
	if selected == nil {
		return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrNotFound)
	}
	selected.mu.RLock()
	info := selected.snapshot
	selected.mu.RUnlock()
	return b.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: selected.ID().String(), Title: info.Title, PID: info.PID, AppID: info.AppID, Bounds: accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H}, Active: info.Active})
}

// Find returns nodes matching a bounded case-insensitive query.
func (b *AccessibilityBundle) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	if err := b.checkAvailable("find"); err != nil {
		return nil, err
	}
	nodes, err := b.backend.Find(ctx, root, query, opts)
	return nodes, b.operationError("find", err)
}

// FindOne resolves a unique node and includes bounded candidate context when
// no node or more than one node satisfies the query.
func (b *AccessibilityBundle) FindOne(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	if err := b.checkAvailable("find"); err != nil {
		return accessibility.Node{}, err
	}
	nodes, err := b.backend.Find(ctx, root, query, opts)
	if err != nil {
		return accessibility.Node{}, b.operationError("find", err)
	}
	if len(nodes) == 1 {
		return nodes[0], nil
	}
	snapshot, snapshotErr := b.backend.Snapshot(ctx, root, opts)
	if snapshotErr != nil {
		return accessibility.Node{}, b.operationError("find", snapshotErr)
	}
	candidates := accessibility.CandidatesForQuery(snapshot, query, 32)
	if len(nodes) == 0 {
		return accessibility.Node{}, b.operationError("find", &accessibility.MatchError{Operation: "find", Err: accessibility.ErrNotFound, Candidates: candidates})
	}
	for _, node := range nodes {
		candidate := accessibility.Candidate{ID: node.ID, Role: node.Role, Name: node.Name, States: node.States, Actions: node.Actions, Bounds: node.Bounds, HasBounds: node.HasBounds, Visible: node.Visible, Showing: node.Showing, Enabled: node.Enabled, Relations: node.Relations, Rejection: "multiple nodes matched"}
		candidates = append(candidates, candidate)
	}
	return accessibility.Node{}, b.operationError("find", &accessibility.MatchError{Operation: "find", Err: accessibility.ErrAmbiguous, Candidates: candidates})
}

// Focused returns the currently focused accessible object.
func (b *AccessibilityBundle) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	if err := b.checkAvailable("focused"); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.backend.Focused(ctx, opts)
	return node, b.operationError("focused", err)
}

// AtPoint returns the accessible object at a screen coordinate.
func (b *AccessibilityBundle) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	if err := b.checkAvailable("at-point"); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.backend.AtPoint(ctx, x, y)
	return node, b.operationError("at-point", err)
}

// FindApplication selects a single application by accessible name, PID, or
// AT-SPI bus name when the runtime backend exposes process ownership.
func (b *AccessibilityBundle) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) { //nolint:gocyclo // scope validation and correlation are intentionally explicit.
	if err := b.checkAvailable("find-application"); err != nil {
		return accessibility.Application{}, err
	}
	finder, ok := b.backend.(accessibility.ApplicationFinder)
	if !ok {
		return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupported)
	}
	// Window selectors are correlated through the session's authoritative
	// window manager. AT-SPI application object paths are not window handles,
	// and titles exposed by an application root are not reliable window IDs.
	var selectedWindow *Window
	if filter.WindowID != "" || filter.WindowTitle != "" {
		if b.session == nil || b.session.Windows == nil {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupported)
		}
		windows, listErr := b.session.Windows.List(ctx, WindowMatch{})
		if listErr != nil {
			return accessibility.Application{}, b.operationError("find-application", listErr)
		}
		wantID := strings.TrimSpace(filter.WindowID)
		wantTitle := strings.TrimSpace(filter.WindowTitle)
		for _, candidate := range windows {
			candidate.mu.RLock()
			candidateInfo := candidate.snapshot
			candidate.mu.RUnlock()
			if wantID != "" && candidate.ID().String() != wantID {
				continue
			}
			if wantTitle != "" && candidateInfo.Title != wantTitle {
				continue
			}
			if selectedWindow != nil {
				return accessibility.Application{}, b.operationError("find-application", accessibility.ErrAmbiguous)
			}
			selectedWindow = candidate
		}
		if selectedWindow == nil {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrNotFound)
		}
	}
	backendFilter := filter
	backendFilter.WindowID = ""
	backendFilter.WindowTitle = ""
	app, err := finder.FindApplication(ctx, backendFilter)
	if err == nil && selectedWindow != nil {
		selectedWindow.mu.RLock()
		selectedInfo := selectedWindow.snapshot
		selectedWindow.mu.RUnlock()
		resolver, resolverOK := b.backend.(accessibility.WindowResolver)
		if !resolverOK {
			err = accessibility.ErrUnsupported
		} else {
			_, resolveErr := resolver.ResolveWindow(ctx, accessibility.WindowTarget{ID: selectedWindow.ID().String(), Title: selectedInfo.Title, PID: selectedInfo.PID, AppID: selectedInfo.AppID, Bounds: accessibility.Rect{X: selectedInfo.X, Y: selectedInfo.Y, Width: selectedInfo.W, Height: selectedInfo.H}, Active: selectedInfo.Active})
			err = resolveErr
		}
		if filter.PID != 0 && selectedInfo.PID != 0 && selectedInfo.PID != filter.PID {
			err = accessibility.ErrNotFound
		} else if app.PID != 0 && selectedInfo.PID != 0 && app.PID != selectedInfo.PID {
			err = accessibility.ErrNotFound
		}
	}
	return app, b.operationError("find-application", err)
}

// Events returns the bounded AT-SPI invalidation stream when supported.
func (b *AccessibilityBundle) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.backend.(accessibility.EventSource)
	if !ok {
		return nil, b.operationError("events", accessibility.ErrUnsupported)
	}
	stream, err := source.Events(ctx, opts)
	return stream, b.operationError("events", err)
}

func (b *AccessibilityBundle) automation(operation string) (accessibility.Automation, error) {
	if err := b.checkAvailable(operation); err != nil {
		return nil, err
	}
	automation, ok := b.backend.(accessibility.Automation)
	if !ok {
		return nil, b.operationError(operation, accessibility.ErrUnsupported)
	}
	return automation, nil
}

// InvokeAction invokes a stable AT-SPI action index.
func (b *AccessibilityBundle) InvokeAction(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("invoke-action")
	if err != nil {
		return err
	}
	return b.operationError("invoke-action", a.InvokeAction(ctx, id, index))
}

func (b *AccessibilityBundle) InvokeActionByName(ctx context.Context, id accessibility.NodeID, name string) error {
	a, err := b.automation("invoke-action-by-name")
	if err != nil {
		return err
	}
	return b.operationError("invoke-action-by-name", a.InvokeActionByName(ctx, id, name))
}

// InvokeDefaultAction invokes the first stable action and reports which one
// was selected, making the convenience behavior observable.
func (b *AccessibilityBundle) InvokeDefaultAction(ctx context.Context, id accessibility.NodeID) (accessibility.Action, error) {
	a, err := b.automation("invoke-default-action")
	if err != nil {
		return accessibility.Action{}, err
	}
	chosen, callErr := a.InvokeDefaultAction(ctx, id)
	return chosen, b.operationError("invoke-default-action", callErr)
}

func (b *AccessibilityBundle) GrabFocus(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("grab-focus")
	if err != nil {
		return err
	}
	return b.operationError("grab-focus", a.GrabFocus(ctx, id))
}
func (b *AccessibilityBundle) ScrollTo(ctx context.Context, id accessibility.NodeID, kind accessibility.ScrollType) error {
	a, err := b.automation("scroll")
	if err != nil {
		return err
	}
	return b.operationError("scroll", a.ScrollTo(ctx, id, kind))
}
func (b *AccessibilityBundle) ScrollToPoint(ctx context.Context, id accessibility.NodeID, kind accessibility.ScrollType, x, y int) error {
	a, err := b.automation("scroll-to-point")
	if err != nil {
		return err
	}
	return b.operationError("scroll-to-point", a.ScrollToPoint(ctx, id, kind, x, y))
}
func (b *AccessibilityBundle) SetCurrentValue(ctx context.Context, id accessibility.NodeID, value float64) error {
	a, err := b.automation("set-current-value")
	if err != nil {
		return err
	}
	return b.operationError("set-current-value", a.SetCurrentValue(ctx, id, value))
}
func (b *AccessibilityBundle) SetValue(ctx context.Context, id accessibility.NodeID, value float64) error {
	a, err := b.automation("set-value")
	if err != nil {
		return err
	}
	return b.operationError("set-value", a.SetValue(ctx, id, value))
}
func (b *AccessibilityBundle) SetTextContents(ctx context.Context, id accessibility.NodeID, value string) error {
	a, err := b.automation("set-text-contents")
	if err != nil {
		return err
	}
	return b.operationError("set-text-contents", a.SetTextContents(ctx, id, value))
}
func (b *AccessibilityBundle) ReplaceText(ctx context.Context, id accessibility.NodeID, start, end int32, value string) error {
	a, err := b.automation("replace-text")
	if err != nil {
		return err
	}
	return b.operationError("replace-text", a.ReplaceText(ctx, id, start, end, value))
}
func (b *AccessibilityBundle) InsertText(ctx context.Context, id accessibility.NodeID, offset int32, value string) error {
	a, err := b.automation("insert-text")
	if err != nil {
		return err
	}
	return b.operationError("insert-text", a.InsertText(ctx, id, offset, value))
}
func (b *AccessibilityBundle) DeleteText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("delete-text")
	if err != nil {
		return err
	}
	return b.operationError("delete-text", a.DeleteText(ctx, id, start, end))
}
func (b *AccessibilityBundle) CopyText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("copy-text")
	if err != nil {
		return err
	}
	return b.operationError("copy-text", a.CopyText(ctx, id, start, end))
}
func (b *AccessibilityBundle) CutText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("cut-text")
	if err != nil {
		return err
	}
	return b.operationError("cut-text", a.CutText(ctx, id, start, end))
}
func (b *AccessibilityBundle) PasteText(ctx context.Context, id accessibility.NodeID, positions ...int32) error {
	a, err := b.automation("paste-text")
	if err != nil {
		return err
	}
	position := int32(0)
	if len(positions) > 0 {
		position = positions[0]
	}
	return b.operationError("paste-text", a.PasteText(ctx, id, position))
}
func (b *AccessibilityBundle) SetCaretOffset(ctx context.Context, id accessibility.NodeID, offset int32) error {
	a, err := b.automation("set-caret")
	if err != nil {
		return err
	}
	return b.operationError("set-caret", a.SetCaretOffset(ctx, id, offset))
}
func (b *AccessibilityBundle) SetTextSelection(ctx context.Context, id accessibility.NodeID, offsets ...int32) error {
	a, err := b.automation("set-text-selection")
	if err != nil {
		return err
	}
	if len(offsets) == 2 {
		offsets = []int32{0, offsets[0], offsets[1]}
	}
	if len(offsets) != 3 {
		return b.operationError("set-text-selection", fmt.Errorf("accessibility: SetTextSelection expects start,end or selection,start,end"))
	}
	return b.operationError("set-text-selection", a.SetTextSelection(ctx, id, offsets[0], offsets[1], offsets[2]))
}
func (b *AccessibilityBundle) AddTextSelection(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("add-text-selection")
	if err != nil {
		return err
	}
	return b.operationError("add-text-selection", a.AddTextSelection(ctx, id, start, end))
}
func (b *AccessibilityBundle) RemoveTextSelection(ctx context.Context, id accessibility.NodeID, selection int32) error {
	a, err := b.automation("remove-text-selection")
	if err != nil {
		return err
	}
	return b.operationError("remove-text-selection", a.RemoveTextSelection(ctx, id, selection))
}
func (b *AccessibilityBundle) SelectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("select-child")
	if err != nil {
		return err
	}
	return b.operationError("select-child", a.SelectChild(ctx, id, index))
}
func (b *AccessibilityBundle) DeselectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("deselect-child")
	if err != nil {
		return err
	}
	return b.operationError("deselect-child", a.DeselectChild(ctx, id, index))
}
func (b *AccessibilityBundle) SelectAll(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("select-all")
	if err != nil {
		return err
	}
	return b.operationError("select-all", a.SelectAll(ctx, id))
}
func (b *AccessibilityBundle) ClearSelection(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("clear-selection")
	if err != nil {
		return err
	}
	return b.operationError("clear-selection", a.ClearSelection(ctx, id))
}
func (b *AccessibilityBundle) DeselectAll(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("deselect-all")
	if err != nil {
		return err
	}
	return b.operationError("deselect-all", a.DeselectAll(ctx, id))
}
func (b *AccessibilityBundle) SelectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	a, err := b.automation("select-row")
	if err != nil {
		return err
	}
	return b.operationError("select-row", a.SelectRow(ctx, id, row))
}
func (b *AccessibilityBundle) DeselectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	a, err := b.automation("deselect-row")
	if err != nil {
		return err
	}
	return b.operationError("deselect-row", a.DeselectRow(ctx, id, row))
}
func (b *AccessibilityBundle) SelectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	a, err := b.automation("select-column")
	if err != nil {
		return err
	}
	return b.operationError("select-column", a.SelectColumn(ctx, id, column))
}
func (b *AccessibilityBundle) DeselectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	a, err := b.automation("deselect-column")
	if err != nil {
		return err
	}
	return b.operationError("deselect-column", a.DeselectColumn(ctx, id, column))
}

// FocusNode and ScrollNodeIntoView are explicit AT-SPI convenience helpers.
func (b *AccessibilityBundle) FocusNode(ctx context.Context, id accessibility.NodeID) error {
	return b.GrabFocus(ctx, id)
}
func (b *AccessibilityBundle) ScrollNodeIntoView(ctx context.Context, id accessibility.NodeID) error {
	return b.ScrollTo(ctx, id, accessibility.ScrollAnyWhere)
}
func (b *AccessibilityBundle) ReplaceEditableText(ctx context.Context, id accessibility.NodeID, value string) error {
	return b.SetTextContents(ctx, id, value)
}

// ReopenAccessibility explicitly reconnects to the same target session.
func (b *AccessibilityBundle) ReopenAccessibility(ctx context.Context) error {
	if b == nil {
		return ErrNilSession
	}
	if err := b.checkAvailable("reopen"); err != nil {
		return err
	}
	reopener, ok := b.backend.(accessibility.Reopener)
	if !ok {
		return b.operationError("reopen", accessibility.ErrUnsupported)
	}
	fresh, err := reopener.Reopen(ctx)
	if err != nil {
		return b.operationError("reopen", err)
	}
	old := b.backend
	b.backend = fresh
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Generation returns the current accessibility invalidation generation.
func (b *AccessibilityBundle) Generation() uint64 {
	if b == nil || util.IsNil(b.backend) {
		return 0
	}
	if source, ok := b.backend.(accessibility.GenerationSource); ok {
		return source.Generation()
	}
	return 0
}

// Invalidate drops cached accessibility observations when a caller observes a
// state change through another channel.
func (b *AccessibilityBundle) Invalidate(id accessibility.NodeID) {
	if b == nil || util.IsNil(b.backend) {
		return
	}
	if source, ok := b.backend.(accessibility.GenerationSource); ok {
		source.Invalidate(id)
	}
}
