package sessionaccessibility

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"strings"
	"time"
)

// Applications lists application roots currently registered with AT-SPI.
func (b *operationState) Applications(ctx context.Context) ([]accessibility.Application, error) {
	if err := b.checkAvailable("applications"); err != nil {
		return nil, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	apps, err := b.gate.Backend().Applications(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(ctxErr, b.operationError("applications", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return nil, errors.Join(openErr, b.operationError("applications", err))
	}
	return apps, b.operationError("applications", err)
}

// Snapshot returns a bounded accessibility tree. Callers must provide an
// application/window/root handle; a zero root is accepted only with the
// explicit SnapshotOptions.AllowDesktopRoot diagnostic opt-in.
func (b *operationState) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	if err := b.checkAvailable("snapshot"); err != nil {
		return accessibility.Snapshot{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Snapshot{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Snapshot{}, err
	}
	snapshot, err := b.gate.Backend().Snapshot(ctx, root, opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Snapshot{}, errors.Join(ctxErr, b.operationError("snapshot", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Snapshot{}, errors.Join(openErr, b.operationError("snapshot", err))
	}
	return snapshot, b.operationError("snapshot", err)
}

// Outline returns a bounded compact semantic view derived from a scoped
// accessibility snapshot.
func (b *operationState) Outline(ctx context.Context, root accessibility.NodeID, snapshotOptions accessibility.SnapshotOptions, outlineOptions accessibility.OutlineOptions) (accessibility.Outline, error) {
	if err := b.checkAvailable("outline"); err != nil {
		return accessibility.Outline{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Outline{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Outline{}, err
	}
	if source, ok := b.gate.Backend().(interface {
		Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error)
	}); ok {
		outline, err := source.Outline(ctx, root, snapshotOptions, outlineOptions)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return accessibility.Outline{}, errors.Join(ctxErr, b.operationError("outline", err))
		}
		if openErr := b.gate.EnsureOpen(); openErr != nil {
			return accessibility.Outline{}, errors.Join(openErr, b.operationError("outline", err))
		}
		return outline, b.operationError("outline", err)
	}
	snapshot, err := b.gate.Backend().Snapshot(ctx, root, snapshotOptions)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Outline{}, errors.Join(ctxErr, b.operationError("outline", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Outline{}, errors.Join(openErr, b.operationError("outline", err))
	}
	if err != nil {
		return accessibility.Outline{}, b.operationError("outline", err)
	}
	return accessibility.BuildOutline(snapshot, outlineOptions), nil
}

// AccessibilityWindow resolves an explicit compositor/accessibility target to
// its exact AT-SPI top-level subtree. It never substitutes the application
// root. Prefer WindowRoot when the caller has a Perfuncted managed window ID.
func (b *operationState) AccessibilityWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	return b.accessibilityWindow(ctx, target, false)
}

func (b *operationState) accessibilityWindow(ctx context.Context, target accessibility.WindowTarget, fresh bool) (accessibility.WindowScope, error) {
	if err := b.checkAvailable("window-root"); err != nil {
		return accessibility.WindowScope{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.WindowScope{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.WindowScope{}, err
	}
	if fresh {
		resolver, ok := b.gate.Backend().(accessibility.FreshWindowResolver)
		if !ok {
			return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrUnsupported)
		}
		scope, err := resolver.ResolveWindowFresh(ctx, target)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return accessibility.WindowScope{}, errors.Join(ctxErr, b.operationError("window-root", err))
		}
		if openErr := b.gate.EnsureOpen(); openErr != nil {
			return accessibility.WindowScope{}, errors.Join(openErr, b.operationError("window-root", err))
		}
		return scope, b.operationError("window-root", err)
	}
	resolver, ok := b.gate.Backend().(accessibility.WindowResolver)
	if !ok {
		return accessibility.WindowScope{}, b.operationError("window-root", accessibility.ErrUnsupported)
	}
	scope, err := resolver.ResolveWindow(ctx, target)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.WindowScope{}, errors.Join(ctxErr, b.operationError("window-root", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.WindowScope{}, errors.Join(openErr, b.operationError("window-root", err))
	}
	return scope, b.operationError("window-root", err)
}

// Find returns nodes matching a bounded case-insensitive query from one fresh,
// structurally complete traversal. opts.Fresh does not disable the fresh read.
// A truncated or structurally inconsistent traversal returns
// accessibility.ErrIncompleteSnapshot. Use a scoped AccessibilityLocator when
// the result must be guarded against AT-SPI events during resolution and action.
func (b *operationState) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	if err := b.checkAvailable("find"); err != nil {
		return nil, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	opts.Fresh = true
	snapshot, err := b.gate.Backend().Snapshot(ctx, root, opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(ctxErr, b.operationError("find", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return nil, errors.Join(openErr, b.operationError("find", err))
	}
	if err != nil {
		return nil, b.operationError("find", err)
	}
	selector := selectorFromQuery(query)
	if err := accessibility.ValidateSemanticSnapshot(snapshot, selector); err != nil {
		return nil, b.operationError("find", err)
	}
	return accessibility.FilterSnapshotSelector(snapshot, selector), nil
}

// FindOne resolves a unique node from a single bounded snapshot. Candidate
// context comes from the same snapshot so a miss costs one AT-SPI traversal,
// not a find plus a second diagnostic snapshot.
func (b *operationState) FindOne(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	return b.FindOneSelector(ctx, root, selectorFromQuery(query), opts)
}

func selectorFromQuery(query accessibility.Query) accessibility.Selector {
	return accessibility.Selector{
		Name:       query.Name,
		Role:       query.Role,
		Text:       query.Text,
		States:     query.States,
		Attributes: query.Attributes,
	}
}

// FindOneSelector resolves one target through the shared semantic predicate
// path and rejects observations that cannot prove the result is unique.
func (b *operationState) FindOneSelector(ctx context.Context, root accessibility.NodeID, selector accessibility.Selector, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	if err := b.checkAvailable("find"); err != nil {
		return accessibility.Node{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Node{}, err
	}
	opts.Fresh = true
	snapshot, err := b.gate.Backend().Snapshot(ctx, root, opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Node{}, errors.Join(ctxErr, b.operationError("find", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Node{}, errors.Join(openErr, b.operationError("find", err))
	}
	if err != nil {
		return accessibility.Node{}, b.operationError("find", err)
	}
	query := accessibility.Query{Name: selector.Name, Role: selector.Role, Text: selector.Text, States: selector.States, Attributes: selector.Attributes}
	if err := accessibility.ValidateSemanticSnapshot(snapshot, selector); err != nil {
		return accessibility.Node{}, b.operationError("find", &accessibility.MatchError{Operation: "find", Err: err, Candidates: accessibility.CandidatesForQuery(snapshot, query, 32)})
	}
	nodes := accessibility.FilterSnapshotSelector(snapshot, selector)
	if len(nodes) == 1 {
		return nodes[0], nil
	}
	candidates := accessibility.CandidatesForQuery(snapshot, query, 32)
	if len(nodes) == 0 {
		return accessibility.Node{}, b.operationError("find", &accessibility.MatchError{Operation: "find", Err: accessibility.ErrNotFound, Candidates: candidates})
	}
	existing := make(map[accessibility.NodeID]struct{}, len(candidates))
	for _, c := range candidates {
		existing[c.ID] = struct{}{}
	}
	for _, node := range nodes {
		if _, dup := existing[node.ID]; dup {
			continue
		}
		candidates = append(candidates, accessibility.Candidate{ID: node.ID, Role: node.Role, Name: node.Name, States: node.States, Actions: node.Actions, Bounds: node.Bounds, HasBounds: node.HasBounds, Visible: node.Visible, Showing: node.Showing, Enabled: node.Enabled, Relations: node.Relations, Rejection: "multiple nodes matched"})
	}
	return accessibility.Node{}, b.operationError("find", &accessibility.MatchError{Operation: "find", Err: accessibility.ErrAmbiguous, Candidates: candidates})
}

// InvokeSemanticAction finds exactly one accessible node in root and invokes
// either its named action or its first exposed action. An empty action name
// is the only convenience behavior; callers can inspect the receipt before
// waiting for an independent postcondition.
func (b *operationState) InvokeSemanticAction(
	ctx context.Context,
	root accessibility.NodeID,
	query accessibility.Query,
	actionName string,
	opts accessibility.SnapshotOptions,
) (accessibility.ActionReceipt, error) {
	node, err := b.FindOne(ctx, root, query, opts)
	if err != nil {
		return accessibility.ActionReceipt{}, err
	}
	return b.invokeSemanticActionOnNode(ctx, node, actionName)
}

// InvokeSelectorSemanticAction applies a rich semantic selector before one
// action dispatch. The same matcher is used by locators and discovery.
func (b *operationState) InvokeSelectorSemanticAction(ctx context.Context, root accessibility.NodeID, selector accessibility.Selector, actionName string, opts accessibility.SnapshotOptions) (accessibility.ActionReceipt, error) {
	node, err := b.FindOneSelector(ctx, root, selector, opts)
	if err != nil {
		return accessibility.ActionReceipt{}, err
	}
	return b.invokeSemanticActionOnNode(ctx, node, actionName)
}

func (b *operationState) invokeSemanticActionOnNode(ctx context.Context, node accessibility.Node, actionName string) (accessibility.ActionReceipt, error) {
	name := strings.TrimSpace(actionName)
	var chosen accessibility.Action
	var err error
	if name == "" {
		chosen, err = b.InvokeDefaultAction(ctx, node.ID)
	} else {
		chosen, err = b.InvokeActionByName(ctx, node.ID, name)
	}
	if err != nil {
		// The provider error can carry action metadata. Do not publish a
		// failure receipt once the caller or session has lost authority.
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return accessibility.ActionReceipt{}, errors.Join(ctxErr, err)
			}
		}
		if openErr := b.gate.EnsureOpen(); openErr != nil {
			return accessibility.ActionReceipt{}, errors.Join(openErr, err)
		}
		var dispatchErr *accessibility.ActionInvocationError
		if errors.As(err, &dispatchErr) {
			return accessibility.ActionReceipt{
				Node: node, Action: dispatchErr.Action, Operation: "invoke-action", Mechanism: "at-spi.action",
				Generation: node.ID.Generation, Dispatch: dispatchErr.Dispatch,
				Outcome: accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeNotObserved},
			}, err
		}
		return accessibility.ActionReceipt{}, err
	}
	return accessibility.ActionReceipt{
		Node:         node,
		Action:       chosen,
		Operation:    "invoke-action",
		Mechanism:    "at-spi.action",
		Generation:   node.ID.Generation,
		Dispatch:     accessibility.DispatchAccepted,
		DispatchedAt: time.Now().UTC(),
		Outcome:      accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeNotObserved},
	}, nil
}

// Focused returns the currently focused accessible object.
func (b *operationState) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	if err := b.checkAvailable("focused"); err != nil {
		return accessibility.Node{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.gate.Backend().Focused(ctx, opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Node{}, errors.Join(ctxErr, b.operationError("focused", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Node{}, errors.Join(openErr, b.operationError("focused", err))
	}
	return node, b.operationError("focused", err)
}

// AtPoint returns the accessible object at a screen coordinate.
func (b *operationState) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	if err := b.checkAvailable("at-point"); err != nil {
		return accessibility.Node{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.gate.Backend().AtPoint(ctx, x, y)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Node{}, errors.Join(ctxErr, b.operationError("at-point", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Node{}, errors.Join(openErr, b.operationError("at-point", err))
	}
	return node, b.operationError("at-point", err)
}

// Events returns the bounded AT-SPI invalidation stream when supported. Its
// context owns the stream lifetime; backend setup has its own short protocol
// guard and is not replaced with a fixed stream expiry.
func (b *operationState) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.gate.Backend().(accessibility.EventSource)
	if !ok {
		return nil, b.operationError("events", accessibility.ErrUnsupported)
	}
	// Event streams are long-lived, so do not impose the normal operation
	// timeout. Admission and publication still require caller and session
	// authority, even when a provider ignores cancellation during setup.
	if ctx == nil {
		return nil, b.operationError("events", errors.New("accessibility: nil event context"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	stream, err := source.Events(ctx, opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(ctxErr, b.operationError("events", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return nil, errors.Join(openErr, b.operationError("events", err))
	}
	// An event subscription has only one valid success form: a live channel
	// and no setup error. Never hand an ambiguous stream to the caller.
	if err != nil {
		return nil, b.operationError("events", err)
	}
	if stream == nil {
		return nil, b.operationError("events", accessibility.ErrUnsupported)
	}
	return stream, nil
}

// dispatchMutation only acknowledges one AT-SPI mutation while its caller
// and owning session remain authorized. Backends may ignore cancellation,
// so check both before dispatch and after the backend returns.
func (b *operationState) dispatchMutation(ctx context.Context, operation string, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	backendErr := run()
	if err := ctx.Err(); err != nil {
		return errors.Join(err, b.operationError(operation, backendErr))
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return errors.Join(err, b.operationError(operation, backendErr))
	}
	return b.operationError(operation, backendErr)
}

func (b *operationState) automation(operation string) (accessibility.Automation, error) {
	if err := b.checkAvailable(operation); err != nil {
		return nil, err
	}
	automation, ok := b.gate.Backend().(accessibility.Automation)
	if !ok {
		return nil, b.operationError(operation, accessibility.ErrUnsupported)
	}
	return automation, nil
}

// InvokeAction invokes a stable AT-SPI action index.
func (b *operationState) InvokeAction(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("invoke-action")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "invoke-action", func() error {
		return a.InvokeAction(ctx, id, index)
	})
}

// InvokeActionByName invokes a uniquely named AT-SPI action and returns the
// metadata selected by the same read used to choose its stable index.
func (b *operationState) InvokeActionByName(ctx context.Context, id accessibility.NodeID, name string) (accessibility.Action, error) {
	a, err := b.automation("invoke-action-by-name")
	if err != nil {
		return accessibility.Action{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Action{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Action{}, err
	}
	chosen, callErr := a.InvokeActionByName(ctx, id, name)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Action{}, errors.Join(ctxErr, b.operationError("invoke-action-by-name", callErr))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Action{}, errors.Join(openErr, b.operationError("invoke-action-by-name", callErr))
	}
	return chosen, b.operationError("invoke-action-by-name", callErr)
}

// InvokeDefaultAction invokes the first stable action and reports which one
// was selected, making the convenience behavior observable.
func (b *operationState) InvokeDefaultAction(ctx context.Context, id accessibility.NodeID) (accessibility.Action, error) {
	a, err := b.automation("invoke-default-action")
	if err != nil {
		return accessibility.Action{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Action{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Action{}, err
	}
	chosen, callErr := a.InvokeDefaultAction(ctx, id)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Action{}, errors.Join(ctxErr, b.operationError("invoke-default-action", callErr))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Action{}, errors.Join(openErr, b.operationError("invoke-default-action", callErr))
	}
	return chosen, b.operationError("invoke-default-action", callErr)
}

// ScrollTo scrolls an AT-SPI node into view using the requested alignment.
func (b *operationState) ScrollTo(ctx context.Context, id accessibility.NodeID, kind accessibility.ScrollType) error {
	a, err := b.automation("scroll")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "scroll", func() error {
		return a.ScrollTo(ctx, id, kind)
	})
}

// ScrollToPoint scrolls an AT-SPI node to a point in the requested coordinate space.
func (b *operationState) ScrollToPoint(ctx context.Context, id accessibility.NodeID, kind accessibility.CoordType, x, y int) error {
	a, err := b.automation("scroll-to-point")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "scroll-to-point", func() error {
		return a.ScrollToPoint(ctx, id, kind, x, y)
	})
}

// SetPosition moves an AT-SPI Component in the requested coordinate space.
func (b *operationState) SetPosition(ctx context.Context, id accessibility.NodeID, x, y int, kind accessibility.CoordType) error {
	a, err := b.automation("set-position")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-position", func() error {
		return a.SetPosition(ctx, id, x, y, kind)
	})
}

// SetSize resizes an AT-SPI Component.
func (b *operationState) SetSize(ctx context.Context, id accessibility.NodeID, width, height int) error {
	a, err := b.automation("set-size")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-size", func() error {
		return a.SetSize(ctx, id, width, height)
	})
}

// SetExtents moves and resizes an AT-SPI Component in the requested coordinate space.
func (b *operationState) SetExtents(ctx context.Context, id accessibility.NodeID, x, y, width, height int, kind accessibility.CoordType) error {
	a, err := b.automation("set-extents")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-extents", func() error {
		return a.SetExtents(ctx, id, x, y, width, height, kind)
	})
}

// SetValue sets the current value exposed by an AT-SPI Value object.
func (b *operationState) SetValue(ctx context.Context, id accessibility.NodeID, value float64) error {
	a, err := b.automation("set-value")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-value", func() error {
		return a.SetValue(ctx, id, value)
	})
}

// InsertText inserts text at a character offset in an AT-SPI editable-text object.
func (b *operationState) InsertText(ctx context.Context, id accessibility.NodeID, offset int32, value string) error {
	a, err := b.automation("insert-text")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "insert-text", func() error {
		return a.InsertText(ctx, id, offset, value)
	})
}

// DeleteText deletes a character range from an AT-SPI editable-text object.
func (b *operationState) DeleteText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("delete-text")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "delete-text", func() error {
		return a.DeleteText(ctx, id, start, end)
	})
}

// CopyText copies a character range from an AT-SPI editable-text object.
func (b *operationState) CopyText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("copy-text")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "copy-text", func() error {
		return a.CopyText(ctx, id, start, end)
	})
}

// CutText cuts a character range from an AT-SPI editable-text object.
func (b *operationState) CutText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("cut-text")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "cut-text", func() error {
		return a.CutText(ctx, id, start, end)
	})
}

// PasteText pastes clipboard text at a character offset in an AT-SPI editable-text object.
func (b *operationState) PasteText(ctx context.Context, id accessibility.NodeID, position int32) error {
	a, err := b.automation("paste-text")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "paste-text", func() error {
		return a.PasteText(ctx, id, position)
	})
}

// SetCaretOffset sets the caret character offset in an AT-SPI text object.
func (b *operationState) SetCaretOffset(ctx context.Context, id accessibility.NodeID, offset int32) error {
	a, err := b.automation("set-caret")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-caret", func() error {
		return a.SetCaretOffset(ctx, id, offset)
	})
}

// SetTextSelections replaces a document's cross-object selections (AT-SPI 2.52).
func (b *operationState) SetTextSelections(ctx context.Context, id accessibility.NodeID, selections []accessibility.DocumentTextSelection) error {
	a, err := b.automation("set-document-text-selections")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-document-text-selections", func() error {
		return a.SetTextSelections(ctx, id, selections)
	})
}

// SetTextSelection sets selection N to a character-offset range in an AT-SPI
// text object. All three arguments are required; there is no implicit
// selection zero.
func (b *operationState) SetTextSelection(ctx context.Context, id accessibility.NodeID, selection, start, end int32) error {
	a, err := b.automation("set-text-selection")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-text-selection", func() error {
		return a.SetTextSelection(ctx, id, selection, start, end)
	})
}

// AddTextSelection adds a character-offset range to an AT-SPI text object.
func (b *operationState) AddTextSelection(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	a, err := b.automation("add-text-selection")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "add-text-selection", func() error {
		return a.AddTextSelection(ctx, id, start, end)
	})
}

// RemoveTextSelection removes a selection from an AT-SPI text object.
func (b *operationState) RemoveTextSelection(ctx context.Context, id accessibility.NodeID, selection int32) error {
	a, err := b.automation("remove-text-selection")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "remove-text-selection", func() error {
		return a.RemoveTextSelection(ctx, id, selection)
	})
}

// SelectChild selects a child in an AT-SPI selection object.
func (b *operationState) SelectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("select-child")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "select-child", func() error {
		return a.SelectChild(ctx, id, index)
	})
}

// DeselectChild deselects a child in an AT-SPI selection object.
func (b *operationState) DeselectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	a, err := b.automation("deselect-child")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "deselect-child", func() error {
		return a.DeselectChild(ctx, id, index)
	})
}

// SelectAll selects all children in an AT-SPI selection object.
func (b *operationState) SelectAll(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("select-all")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "select-all", func() error {
		return a.SelectAll(ctx, id)
	})
}

// ClearSelection clears all children in an AT-SPI selection object.
func (b *operationState) ClearSelection(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("clear-selection")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "clear-selection", func() error {
		return a.ClearSelection(ctx, id)
	})
}

// DeselectSelectedChild deselects the currently selected child in an AT-SPI Selection object.
func (b *operationState) DeselectSelectedChild(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("deselect-selected-child")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "deselect-selected-child", func() error {
		return a.DeselectSelectedChild(ctx, id)
	})
}

// SelectRow selects a row in an AT-SPI table selection object.
func (b *operationState) SelectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	a, err := b.automation("select-row")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "select-row", func() error {
		return a.SelectRow(ctx, id, row)
	})
}

// DeselectRow deselects a row in an AT-SPI table selection object.
func (b *operationState) DeselectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	a, err := b.automation("deselect-row")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "deselect-row", func() error {
		return a.DeselectRow(ctx, id, row)
	})
}

// SelectColumn selects a column in an AT-SPI table selection object.
func (b *operationState) SelectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	a, err := b.automation("select-column")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "select-column", func() error {
		return a.SelectColumn(ctx, id, column)
	})
}

// DeselectColumn deselects a column in an AT-SPI table selection object.
func (b *operationState) DeselectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	a, err := b.automation("deselect-column")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "deselect-column", func() error {
		return a.DeselectColumn(ctx, id, column)
	})
}

// FocusNode requests focus for a semantic node.
func (b *operationState) FocusNode(ctx context.Context, id accessibility.NodeID) error {
	a, err := b.automation("grab-focus")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "grab-focus", func() error {
		return a.GrabFocus(ctx, id)
	})
}

// ScrollNodeIntoView scrolls a node into view with default alignment.
func (b *operationState) ScrollNodeIntoView(ctx context.Context, id accessibility.NodeID) error {
	return b.ScrollTo(ctx, id, accessibility.ScrollAnyWhere)
}

// ReplaceEditableText replaces all contents of an editable text node.
func (b *operationState) ReplaceEditableText(ctx context.Context, id accessibility.NodeID, value string) error {
	a, err := b.automation("set-text-contents")
	if err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	return b.dispatchMutation(ctx, "set-text-contents", func() error {
		return a.SetTextContents(ctx, id, value)
	})
}

// Reopen explicitly reconnects the accessibility bundle to the same target
// session. Reconnection remains caller-directed; it does not happen in the
// background.
func (b *operationState) Reopen(ctx context.Context) error {
	if b == nil {
		return capability.ErrNilSession
	}
	if err := b.checkAvailable("reopen"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	owner, ok := b.gate.Backend().(*Owner)
	if !ok {
		return b.operationError("reopen", accessibility.ErrUnsupported)
	}
	return b.operationError("reopen", owner.Reopen(ctx))
}

// Generation returns the current accessibility invalidation generation.
func (b *operationState) Generation() uint64 {
	if b == nil || util.IsNil(b.gate.Backend()) || (b.gate.IsClosed()) {
		return 0
	}
	if source, ok := b.gate.Backend().(accessibility.GenerationSource); ok {
		generation := source.Generation()
		if b.gate.IsClosed() {
			return 0
		}
		return generation
	}
	return 0
}

// Invalidate drops cached accessibility observations when a caller observes a
// state change through another channel.
func (b *operationState) Invalidate(id accessibility.NodeID) {
	if b == nil || util.IsNil(b.gate.Backend()) || (b.gate.IsClosed()) {
		return
	}
	if source, ok := b.gate.Backend().(accessibility.GenerationSource); ok {
		source.Invalidate(id)
	}
}

func (b *operationState) findApplicationDirect(ctx context.Context, filter accessibility.ApplicationFilter, fresh bool) (accessibility.Application, error) {
	finder, ok := b.gate.Backend().(accessibility.ApplicationFinder)
	if !ok {
		return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupported)
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return accessibility.Application{}, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return accessibility.Application{}, err
	}
	var app accessibility.Application
	var err error
	if fresh {
		freshFinder, supported := b.gate.Backend().(accessibility.FreshApplicationFinder)
		if !supported {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupported)
		}
		app, err = freshFinder.FindApplicationFresh(ctx, filter)
	} else {
		app, err = finder.FindApplication(ctx, filter)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return accessibility.Application{}, errors.Join(ctxErr, b.operationError("find-application", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return accessibility.Application{}, errors.Join(openErr, b.operationError("find-application", err))
	}
	return app, b.operationError("find-application", err)
}

// OperationAdmission exposes only the accessibility provider and its dynamic
// operation policy. Generation ownership remains with Owner.
type OperationAdmission interface {
	capability.Authority
	Backend() accessibility.Backend
	CheckAvailable(string) error
	OperationContext(context.Context) (context.Context, context.CancelFunc)
	IsClosed() bool
}
type operationState struct{ gate OperationAdmission }
type Operations struct{ *operationState }
type Handle struct{ *operationState }

func NewOperations(gate OperationAdmission) *Operations {
	return &Operations{&operationState{gate: gate}}
}
func (o *Operations) Handle() Handle {
	if o == nil {
		return Handle{}
	}
	return Handle{o.operationState}
}
func (b *operationState) checkAvailable(op string) error {
	if b == nil || b.gate == nil {
		return capability.ErrNilSession
	}
	return b.gate.CheckAvailable(op)
}
func (b *operationState) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return b.gate.OperationContext(ctx)
}
func (b *operationState) operationError(op string, err error) error {
	return b.gate.OperationError(op, err)
}
func (o *Operations) ResolveWindow(ctx context.Context, target accessibility.WindowTarget, fresh bool) (accessibility.WindowScope, error) {
	if o == nil {
		return accessibility.WindowScope{}, capability.ErrNilSession
	}
	return o.accessibilityWindow(ctx, target, fresh)
}
func (o *Operations) InvokeOnNode(ctx context.Context, node accessibility.Node, name string) (accessibility.ActionReceipt, error) {
	if o == nil {
		return accessibility.ActionReceipt{}, capability.ErrNilSession
	}
	return o.invokeSemanticActionOnNode(ctx, node, name)
}
func (o *Operations) FindApplicationDirect(ctx context.Context, filter accessibility.ApplicationFilter, fresh bool) (accessibility.Application, error) {
	return o.findApplicationDirect(ctx, filter, fresh)
}
