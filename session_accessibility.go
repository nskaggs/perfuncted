package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/sessionaccessibility"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/window"
)

var openAccessibility = accessibility.OpenRuntimeContext

// AccessibilityBundle exposes AT-SPI queries and typed automation for the active session.
// It is optional because many headless or minimal desktop sessions do not run
// an accessibility bus.
type accessibilityState struct {
	accessibilityHandle
	operations *sessionaccessibility.Operations
	backend    accessibility.Backend
	bundleBase
}

func (b *accessibilityState) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	owner, ok := b.backend.(*accessibilityBackendOwner)
	if ok {
		return b.bundleBase.checkAvailable(operation, owner.HasBackend())
	}
	return b.checkBackend(operation, b.backend)
}

func (b *accessibilityState) installBackend(backend accessibility.Backend) {
	if b == nil {
		if !util.IsNil(backend) {
			_ = backend.Close()
		}
		return
	}
	if owner, ok := b.backend.(*accessibilityBackendOwner); ok {
		owner.Install(backend)
	} else if !util.IsNil(backend) {
		_ = backend.Close()
	}
}

func (b *accessibilityState) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return b.backendContext(ctx, b.session.Timeouts().Long)
}

func (b *accessibilityState) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}

// WindowRoot is the preferred high-level scope path. It resolves a managed
// window by its authoritative native ID and correlates that window to its
// exact AT-SPI top-level subtree.
func (b *accessibilityState) WindowRoot(ctx context.Context, windowID string) (accessibility.WindowScope, error) {
	return b.windowRoot(ctx, windowID, false)
}

// WindowRootFresh re-correlates a managed window from uncached AT-SPI state.
// It is used by semantic locators so the scope root and matched descendants
// are observed from the same current generation.
func (b *accessibilityState) WindowRootFresh(ctx context.Context, windowID string) (accessibility.WindowScope, error) {
	return b.windowRoot(ctx, windowID, true)
}

func (b *accessibilityState) windowRoot(ctx context.Context, windowID string, fresh bool) (accessibility.WindowScope, error) {
	selected, err := b.resolveManagedWindow(ctx, windowID, "", "window-root")
	if err != nil {
		return accessibility.WindowScope{}, err
	}
	return b.accessibilityWindow(ctx, windowTargetFor(selected, 0), fresh)
}

// FindApplication selects a single application by accessible name, PID, or
// AT-SPI bus name when the runtime backend exposes process ownership.
func (b *accessibilityState) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.findApplication(ctx, filter, false)
}

// FindApplicationFresh selects an application from current backend identity
// state when supported by the runtime.
func (b *accessibilityState) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.findApplication(ctx, filter, true)
}

func (b *accessibilityState) findApplication(ctx context.Context, filter accessibility.ApplicationFilter, fresh bool) (accessibility.Application, error) { //nolint:gocyclo // window and app filters share the same ownership and ambiguity rules.
	if err := b.checkAvailable("find-application"); err != nil {
		return accessibility.Application{}, err
	}
	if filter.WindowID != "" || filter.WindowTitle != "" {
		selectedWindow, err := b.resolveManagedWindow(ctx, filter.WindowID, filter.WindowTitle, "find-application")
		if err != nil {
			return accessibility.Application{}, err
		}
		selectedInfo := selectedWindow.CachedInfo()
		if filter.PID != 0 && selectedInfo.PID != 0 && filter.PID != selectedInfo.PID {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrNotFound)
		}
		scope, err := b.accessibilityWindow(ctx, windowTargetFor(selectedWindow, filter.PID), fresh)
		if err != nil {
			return accessibility.Application{}, b.operationError("find-application", err)
		}
		app := scope.Application
		if app.ID.BusName == "" || app.ID.ObjectPath == "" || app.ID.Generation == 0 {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupportedCorrelation)
		}
		if want := strings.ToLower(strings.TrimSpace(filter.Name)); want != "" && !strings.Contains(strings.ToLower(app.Name), want) {
			return accessibility.Application{}, b.operationError("find-application", accessibility.ErrNotFound)
		}
		return app, nil
	}
	return b.operations.FindApplicationDirect(ctx, filter, fresh)
}

// resolveManagedWindow selects exactly one managed window by native ID and/or
// exact title. It is the single authority for compositor-window selection
// across WindowRoot, FindApplication, and CLI scope resolution. The operation
// label is caller-owned so error messages reflect the actual entry point.
func (b *accessibilityState) resolveManagedWindow(ctx context.Context, windowID, windowTitle, operation string) (*windowState, error) {
	if b == nil {
		return nil, ErrNilSession
	}
	if b.session == nil || b.session.windows == nil {
		return nil, b.operationError(operation, accessibility.ErrUnsupported)
	}
	windows, err := b.session.windows.List(ctx, window.Match{})
	if err != nil {
		return nil, b.operationError(operation, err)
	}
	wantID, wantTitle := strings.TrimSpace(windowID), strings.TrimSpace(windowTitle)
	var selected *windowState
	for _, candidate := range windows {
		info := candidate.CachedInfo()
		if wantID != "" && candidate.ID().String() != wantID {
			continue
		}
		if wantTitle != "" && info.Title != wantTitle {
			continue
		}
		if selected != nil {
			return nil, b.operationError(operation, accessibility.ErrAmbiguous)
		}
		selected = candidate
	}
	if selected == nil {
		return nil, b.operationError(operation, accessibility.ErrNotFound)
	}
	return selected, nil
}

// windowTargetFor converts an authoritative managed window handle into the
// AT-SPI correlation target. PID, title, geometry, and active state are the
// correlation evidence; PIDHint carries a caller-supplied PID only when the
// compositor did not report one.
func windowTargetFor(selected *windowState, pidHint int32) accessibility.WindowTarget {
	info := selected.CachedInfo()
	target := accessibility.WindowTarget{ID: selected.ID().String(), Title: info.Title, PID: info.PID, AppID: info.AppID, Bounds: accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H}, Active: info.Active}
	if target.PID == 0 {
		target.PIDHint = pidHint
	}
	return target
}

type accessibilityHandle = sessionaccessibility.Handle

func newAccessibilityState(base bundleBase, backend accessibility.Backend) *accessibilityState {
	b := &accessibilityState{bundleBase: base, backend: backend}
	b.operations = sessionaccessibility.NewOperations(accessibilityOperationAdmission{b})
	b.accessibilityHandle = b.operations.Handle()
	return b
}

type accessibilityOperationAdmission struct{ state *accessibilityState }

func (a accessibilityOperationAdmission) Backend() accessibility.Backend { return a.state.backend }
func (a accessibilityOperationAdmission) CheckAvailable(op string) error {
	return a.state.checkAvailable(op)
}
func (a accessibilityOperationAdmission) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return a.state.operationContext(ctx)
}
func (a accessibilityOperationAdmission) EnsureOpen() error { return a.state.session.ensureOpen() }
func (a accessibilityOperationAdmission) OperationError(op string, err error) error {
	return a.state.operationError(op, err)
}
func (a accessibilityOperationAdmission) IsClosed() bool {
	return a.state.session != nil && a.state.session.isClosed()
}
func (b *accessibilityState) accessibilityWindow(ctx context.Context, target accessibility.WindowTarget, fresh bool) (accessibility.WindowScope, error) {
	if b == nil {
		return accessibility.WindowScope{}, ErrNilSession
	}
	return b.operations.ResolveWindow(ctx, target, fresh)
}
func (b *accessibilityState) invokeSemanticActionOnNode(ctx context.Context, node accessibility.Node, name string) (accessibility.ActionReceipt, error) {
	if b == nil {
		return accessibility.ActionReceipt{}, ErrNilSession
	}
	return b.operations.InvokeOnNode(ctx, node, name)
}

// ActionVerificationError reports that an accessibility action was dispatched
// but its independent postcondition could not be verified. The action is never
// replayed because of this error.
type ActionVerificationError struct {
	Dispatch  accessibility.DispatchOutcome
	Condition string
	Err       error
}

type actionTargetCondition interface {
	validateActionTarget(accessibility.NodeID, accessibility.Node) error
}

func (e *ActionVerificationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("perfuncted: action dispatch %s; verification of %q failed: %v", e.Dispatch, e.Condition, e.Err)
}

func (e *ActionVerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// InvokeAccessibilityActionAndWait invokes one explicitly resolved AT-SPI
// action and waits for an independent authoritative postcondition. The
// returned evidence describes only bounded wakeups; callers decide what the
// postcondition means.
func (s *sessionCore) InvokeAccessibilityActionAndWait(
	ctx context.Context,
	root accessibility.NodeID,
	query accessibility.Query,
	actionName string,
	snapshotOptions accessibility.SnapshotOptions,
	postcondition Condition,
	waitOptions ...WaitOption,
) (accessibility.ActionReceipt, WaitEvidence, error) {
	if s == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, ErrNilSession
	}
	config, err := s.prepareWait(ctx, postcondition, waitOptions, "accessibility-action-verification")
	if err != nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, err
	}
	if s.accessibility == nil || !s.Has(CapabilityAccessibility) {
		return accessibility.ActionReceipt{}, WaitEvidence{}, &CapabilityError{
			Capability: CapabilityAccessibility,
			Operation:  "invoke-action-and-wait",
			Err:        ErrUnavailable,
		}
	}
	var receipt accessibility.ActionReceipt
	if targetCondition, ok := postcondition.(actionTargetCondition); ok {
		var node accessibility.Node
		node, err = s.accessibility.FindOne(ctx, root, query, snapshotOptions)
		if err == nil {
			err = targetCondition.validateActionTarget(root, node)
		}
		if err == nil {
			receipt, err = s.accessibility.invokeSemanticActionOnNode(ctx, node, actionName)
		}
	} else {
		receipt, err = s.accessibility.InvokeSemanticAction(ctx, root, query, actionName, snapshotOptions)
	}
	if err != nil {
		return receipt, WaitEvidence{}, err
	}
	evidence, err := s.waitWithEvidence(ctx, postcondition, config)
	receipt.Outcome = accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeNotVerified, Condition: postcondition.describe()}
	if err == nil {
		receipt.Outcome.Status = accessibility.ActionOutcomeVerified
		receipt.Outcome.ObservedAt = time.Now().UTC()
	} else {
		err = &ActionVerificationError{Dispatch: receipt.Dispatch, Condition: postcondition.describe(), Err: err}
	}
	return receipt, evidence, err
}

// InvokeAccessibilityActionAndWait invokes one explicitly resolved AT-SPI
// action and waits for an independent authoritative postcondition. The
// returned evidence describes only bounded wakeups; callers decide what the
// postcondition means.
func (s *Session) invokeAccessibilityActionAndWaitContext(
	ctx context.Context,
	root accessibility.NodeID,
	query accessibility.Query,
	actionName string,
	snapshotOptions accessibility.SnapshotOptions,
	postcondition Condition,
	waitOptions ...WaitOption,
) (accessibility.ActionReceipt, WaitEvidence, error) {
	return s.coreState().InvokeAccessibilityActionAndWait(ctx, root, query, actionName, snapshotOptions, postcondition, waitOptions...)
}

// WindowRoot resolves a managed window's exact accessibility subtree.
func (b AccessibilityBundle) windowRootContext(ctx context.Context, id string) (accessibility.WindowScope, error) {
	return b.state.WindowRoot(ctx, id)
}

// WindowRootFresh re-correlates a managed window from fresh state.
func (b AccessibilityBundle) windowRootFreshContext(ctx context.Context, id string) (accessibility.WindowScope, error) {
	return b.state.WindowRootFresh(ctx, id)
}

// FindApplication resolves a unique application in the owning session.
func (b AccessibilityBundle) findApplicationContext(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.state.FindApplication(ctx, filter)
}

// FindApplicationFresh resolves an application from uncached identity state.
func (b AccessibilityBundle) findApplicationFreshContext(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.state.FindApplicationFresh(ctx, filter)
}

// LocatorForWindow scopes a semantic locator to a managed window.
func (b AccessibilityBundle) LocatorForWindow(id string, selector accessibility.Selector, opts accessibility.SnapshotOptions) *AccessibilityLocator {
	loc := b.state.LocatorForWindow(id, selector, opts)
	if loc != nil {
		loc.operation = b.scope
	}
	return loc
}

// LocatorForApplication scopes a semantic locator to a unique application.
func (b AccessibilityBundle) LocatorForApplication(filter accessibility.ApplicationFilter, selector accessibility.Selector, opts accessibility.SnapshotOptions) *AccessibilityLocator {
	loc := b.state.LocatorForApplication(filter, selector, opts)
	if loc != nil {
		loc.operation = b.scope
	}
	return loc
}

const locatorResolutionAttempts = 2

// errLocatorUnverifiable distinguishes a safety-critical locator target from
// generic existence queries, which may retry transient incomplete snapshots.
var errLocatorUnverifiable = errors.New("perfuncted: locator cannot prove a unique semantic target")

type locatorScopeKind uint8

const (
	locatorWindowScope locatorScopeKind = iota + 1
	locatorApplicationScope
)

type locatorScope struct {
	kind        locatorScopeKind
	windowID    string
	application accessibility.ApplicationFilter
}

// AccessibilityLocator is a lazy semantic target scoped to a managed window
// or a uniquely identified application. It stores selector intent and resolves
// provider nodes only when an operation runs. Resolution requires an active
// AT-SPI event stream so provider changes during a fresh snapshot can fail closed.
type AccessibilityLocator struct {
	operation operationScope
	bundle    *accessibilityState
	scope     locatorScope
	selector  accessibility.Selector
	options   accessibility.SnapshotOptions
}

// LocatorForWindow creates a locator rooted in a managed Perfuncted window.
// The window and AT-SPI scope are re-resolved for each operation.
func (b *accessibilityState) LocatorForWindow(windowID string, selector accessibility.Selector, options accessibility.SnapshotOptions) *AccessibilityLocator {
	return &AccessibilityLocator{
		bundle: b, scope: locatorScope{kind: locatorWindowScope, windowID: strings.TrimSpace(windowID)},
		selector: sessionaccessibility.CloneSelector(selector), options: options,
	}
}

// LocatorForApplication creates a locator rooted in an application selected
// by authoritative accessibility identity. Ambiguous application filters fail
// before selector matching.
func (b *accessibilityState) LocatorForApplication(filter accessibility.ApplicationFilter, selector accessibility.Selector, options accessibility.SnapshotOptions) *AccessibilityLocator {
	return &AccessibilityLocator{
		bundle: b, scope: locatorScope{kind: locatorApplicationScope, application: filter},
		selector: sessionaccessibility.CloneSelector(selector), options: options,
	}
}

func (l *AccessibilityLocator) scopeRoot(ctx context.Context) (accessibility.NodeID, sessionaccessibility.LocatorIdentity, error) {
	if l == nil || l.bundle == nil {
		return accessibility.NodeID{}, sessionaccessibility.LocatorIdentity{}, ErrUnavailable
	}
	switch l.scope.kind {
	case locatorWindowScope:
		if l.scope.windowID == "" {
			return accessibility.NodeID{}, sessionaccessibility.LocatorIdentity{}, fmt.Errorf("perfuncted: %w: locator requires a managed window ID", ErrInvalidArgument)
		}
		scope, err := l.bundle.WindowRootFresh(ctx, l.scope.windowID)
		if err != nil {
			return accessibility.NodeID{}, sessionaccessibility.LocatorIdentity{}, err
		}
		return scope.Root, sessionaccessibility.LocatorIdentity{
			WindowID: scope.WindowID, PID: scope.PID, AppID: scope.AppID,
			BusName: scope.Application.ID.BusName, Name: scope.Application.Name,
		}, nil
	case locatorApplicationScope:
		app, err := l.bundle.FindApplicationFresh(ctx, l.scope.application)
		if err != nil {
			return accessibility.NodeID{}, sessionaccessibility.LocatorIdentity{}, err
		}
		return app.ID, sessionaccessibility.LocatorIdentity{PID: app.PID, BusName: app.ID.BusName, Name: app.Name}, nil
	default:
		return accessibility.NodeID{}, sessionaccessibility.LocatorIdentity{}, fmt.Errorf("perfuncted: %w: locator scope is missing", ErrInvalidArgument)
	}
}

func (l *AccessibilityLocator) find(ctx context.Context) ([]accessibility.Node, sessionaccessibility.LocatorIdentity, accessibility.NodeID, context.CancelFunc, error) {
	return l.findTarget(ctx, nil)
}

// findTarget resolves the locator selector until a wait pins a target. Later
// reads can then follow the exact node identity even when a waited-on state,
// accessible value, or other selector property changes.
//
//nolint:gocyclo // This protocol boundary explicitly handles distinct safety and error cases.
func (l *AccessibilityLocator) findTarget(ctx context.Context, targetID *accessibility.NodeID) ([]accessibility.Node, sessionaccessibility.LocatorIdentity, accessibility.NodeID, context.CancelFunc, error) {
	if l == nil || l.bundle == nil {
		return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, ErrUnavailable
	}
	if ctx == nil {
		return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, fmt.Errorf("perfuncted: locator: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, err
	}
	if err := l.bundle.checkAvailable("find"); err != nil {
		return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, err
	}
	var initialIdentity sessionaccessibility.LocatorIdentity
	var initialRoot accessibility.NodeID
	haveInitialIdentity := false
	for attempt := 0; attempt < locatorResolutionAttempts; attempt++ {
		observationCtx, stopObservation := context.WithCancel(ctx)
		stream, err := l.bundle.Events(observationCtx, accessibility.EventOptions{Buffer: 1, RequireSemanticCoverage: true})
		if err != nil {
			stopObservation()
			return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", fmt.Errorf("%w: locator event observation is unavailable: %w", accessibility.ErrObservationChanged, err))
		}
		if observationErr := sessionaccessibility.LocatorObservationReady(stream); observationErr != nil {
			stopObservation()
			return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", observationErr)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			stopObservation()
			return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, ctxErr
		}
		root, identity, err := l.scopeRoot(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			stopObservation()
			return nil, identity, accessibility.NodeID{}, nil, ctxErr
		}
		if err == nil {
			if sessionaccessibility.LocatorIdentityChanged(haveInitialIdentity, initialIdentity, initialRoot, identity, root) {
				stopObservation()
				return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", fmt.Errorf("%w: locator scope identity changed during resolution", accessibility.ErrScope))
			}
			if !haveInitialIdentity {
				initialIdentity = identity
				initialRoot = root
				haveInitialIdentity = true
			}
			// Scope resolution can itself trigger a semantic invalidation.
			// Do not dispatch a snapshot against an already-obsolete scope.
			if observationErr := sessionaccessibility.LocatorObservationReady(stream); observationErr != nil {
				stopObservation()
				return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", observationErr)
			}
			opts := l.options
			opts.Fresh = true
			ctxForRead, cancel := l.bundle.operationContext(ctx)
			snapshot, readErr := l.bundle.backend.Snapshot(ctxForRead, root, opts)
			readContextErr := ctxForRead.Err()
			cancel()
			// A provider may return a complete snapshot concurrently with caller
			// cancellation. Never publish the now-unverifiable target identity.
			if ctxErr := ctx.Err(); ctxErr != nil {
				stopObservation()
				return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, ctxErr
			}
			if openErr := l.bundle.session.ensureOpen(); openErr != nil {
				stopObservation()
				return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, openErr
			}
			if readContextErr != nil {
				stopObservation()
				return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, errors.Join(readContextErr, l.bundle.operationError("find", readErr))
			}
			if observationErr := sessionaccessibility.LocatorObservationReady(stream); observationErr != nil {
				stopObservation()
				return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", errors.Join(observationErr, readErr))
			}
			if readErr == nil {
				validationSelector := l.selector
				if targetID != nil {
					// The selector established identity on the first match. After
					// that, validate the complete tree and resolve the same handle
					// so a requested state/value transition cannot filter it away.
					validationSelector = accessibility.Selector{}
				}
				if incomplete := accessibility.ValidateSemanticSnapshot(snapshot, validationSelector); incomplete != nil {
					stopObservation()
					return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", incomplete)
				}
				var matches []accessibility.Node
				if targetID == nil {
					matches = accessibility.FilterSnapshotSelector(snapshot, l.selector)
				} else {
					for _, node := range snapshot.Nodes {
						if node.ID == *targetID {
							matches = append(matches, node)
						}
					}
				}
				if observationErr := sessionaccessibility.LocatorObservationReady(stream); observationErr != nil {
					stopObservation()
					return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", observationErr)
				}
				if openErr := l.bundle.session.ensureOpen(); openErr != nil {
					stopObservation()
					return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, openErr
				}
				return matches, identity, root, stopObservation, nil
			}
			err = readErr
		}
		if !sessionaccessibility.LocatorResolutionMayRetry(err) {
			stopObservation()
			return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", err)
		}
		if attempt+1 == locatorResolutionAttempts {
			stopObservation()
			return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", err)
		}
		stopObservation()
	}
	return nil, sessionaccessibility.LocatorIdentity{}, accessibility.NodeID{}, nil, accessibility.ErrStaleGeneration
}

// Matches returns all matches from one complete, current snapshot. It preserves
// ambiguity for discovery callers; Resolve applies the unique-target rule.
func (l *AccessibilityLocator) matchesContext(ctx context.Context) ([]accessibility.Node, error) {
	if ctx == nil {
		return nil, fmt.Errorf("perfuncted: locator: %w: nil context", ErrInvalidArgument)
	}
	nodes, _, _, stopObservation, err := l.find(ctx)
	if err != nil {
		return nil, err
	}
	stopObservation()
	return nodes, nil
}

// Resolve requires exactly one match in a complete fresh snapshot.
func (l *AccessibilityLocator) resolveContext(ctx context.Context) (accessibility.Node, error) {
	nodes, err := l.matchesContext(ctx)
	if err != nil {
		return accessibility.Node{}, err
	}
	switch len(nodes) {
	case 0:
		return accessibility.Node{}, accessibility.ErrNotFound
	case 1:
		return nodes[0], nil
	default:
		return accessibility.Node{}, accessibility.ErrAmbiguous
	}
}

// Wait resolves this locator after each authoritative wake or bounded poll.
// Absence is transient; ambiguity and incomplete provider state fail closed.
func (l *AccessibilityLocator) waitContext(ctx context.Context, options ...WaitOption) (accessibility.Node, WaitEvidence, error) {
	return l.waitForState(ctx, AccessibilityWaitPresent, options...)
}

// InvokeActionAndWait resolves one current target, dispatches one semantic
// action, and then waits for an independent caller-supplied postcondition.
func (l *AccessibilityLocator) invokeActionAndWaitContext(ctx context.Context, actionName string, postcondition Condition, options ...WaitOption) (accessibility.ActionReceipt, WaitEvidence, error) { //nolint:gocyclo // unique resolution, dispatch certainty, and bounded stale handling are explicit safety gates.
	return l.actionAndWait(ctx, "action", postcondition, options, func(ctx context.Context, node accessibility.Node) locatorDispatchAttempt {
		receipt, err := l.bundle.invokeSemanticActionOnNode(ctx, node, actionName)
		retryable := err != nil && sessionaccessibility.LocatorActionMayRetryStale(err) &&
			(receipt.Dispatch == "" || receipt.Dispatch == accessibility.DispatchNotSent)
		return locatorDispatchAttempt{receipt: receipt, err: err, retryableNotSent: retryable}
	})
}

// ClickAndWait invokes the target's accessible default action once and records
// whether an independent caller-supplied postcondition becomes true.
func (l *AccessibilityLocator) clickAndWaitContext(ctx context.Context, postcondition Condition, options ...WaitOption) (accessibility.ActionReceipt, WaitEvidence, error) {
	return l.invokeActionAndWaitContext(ctx, "", postcondition, options...)
}

// FillAndWait replaces editable text on the uniquely resolved target with one
// AT-SPI SetTextContents dispatch, then observes an independent postcondition.
// An uncertain provider result is returned with its receipt and is never retried.
func (l *AccessibilityLocator) fillAndWaitContext(ctx context.Context, value string, postcondition Condition, options ...WaitOption) (accessibility.ActionReceipt, WaitEvidence, error) { //nolint:gocyclo // one text mutation, bounded stale handling, and independent outcome proof are explicit gates.
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: nil context", ErrInvalidArgument)
	}
	if postcondition == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: nil postcondition", ErrInvalidArgument)
	}
	if !utf8.ValidString(value) {
		return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: text must be valid UTF-8", ErrInvalidArgument)
	}
	return l.actionAndWait(ctx, "fill", postcondition, options, func(ctx context.Context, node accessibility.Node) locatorDispatchAttempt {
		receipt := accessibility.ActionReceipt{
			Node: node, Operation: "set-text-contents", Mechanism: "at-spi.editable-text",
			Generation: node.ID.Generation, Dispatch: accessibility.DispatchNotSent,
			Outcome: accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeNotObserved},
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return locatorDispatchAttempt{receipt: receipt, err: contextErr}
		}
		err := l.bundle.ReplaceEditableText(ctx, node.ID, value)
		if err == nil {
			receipt.Dispatch = accessibility.DispatchAccepted
			receipt.DispatchedAt = time.Now().UTC()
			return locatorDispatchAttempt{receipt: receipt}
		}
		receipt.Dispatch = sessionaccessibility.TextDispatchOutcome(err)
		return locatorDispatchAttempt{
			receipt: receipt, err: err,
			retryableNotSent: receipt.Dispatch == accessibility.DispatchNotSent && sessionaccessibility.LocatorActionMayRetryStale(err),
		}
	})
}

type locatorDispatchAttempt struct {
	receipt          accessibility.ActionReceipt
	err              error
	retryableNotSent bool
}

func locatorRetryAllowed(attempt locatorDispatchAttempt, attemptNumber int) bool {
	return attempt.err != nil && attempt.retryableNotSent && sessionaccessibility.LocatorActionMayRetryStale(attempt.err) && attemptNumber+1 < locatorResolutionAttempts
}

//nolint:gocyclo // Dispatch, cancellation and independent observation each fail closed.
func (l *AccessibilityLocator) actionAndWait(ctx context.Context, operation string, postcondition Condition, options []WaitOption, dispatch func(context.Context, accessibility.Node) locatorDispatchAttempt) (accessibility.ActionReceipt, WaitEvidence, error) {
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s: %w: nil context", operation, ErrInvalidArgument)
	}
	if postcondition == nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s: %w: nil postcondition", operation, ErrInvalidArgument)
	}
	waitConfig, err := l.bundle.session.prepareWait(ctx, postcondition, options, "accessibility-locator-"+operation+"-verification")
	if err != nil {
		return accessibility.ActionReceipt{}, WaitEvidence{}, err
	}
	var receipt accessibility.ActionReceipt
	for attemptNumber := 0; attemptNumber < locatorResolutionAttempts; attemptNumber++ {
		matches, identity, root, stopObservation, err := l.find(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(ctxErr, err)
			}
			if openErr := l.bundle.session.ensureOpen(); openErr != nil {
				return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(openErr, err)
			}
			return receipt, WaitEvidence{}, err
		}
		node, err := sessionaccessibility.UniqueLocatorMatch(matches)
		if err != nil {
			stopObservation()
			return receipt, WaitEvidence{}, err
		}
		// Resolution does not grant a durable mutation capability. Refuse
		// dispatch after the caller or owning session loses authority.
		if ctxErr := ctx.Err(); ctxErr != nil {
			stopObservation()
			return accessibility.ActionReceipt{}, WaitEvidence{}, ctxErr
		}
		if openErr := l.bundle.session.ensureOpen(); openErr != nil {
			stopObservation()
			return accessibility.ActionReceipt{}, WaitEvidence{}, openErr
		}
		if targetCondition, ok := postcondition.(actionTargetCondition); ok {
			if err := targetCondition.validateActionTarget(root, node); err != nil {
				stopObservation()
				return receipt, WaitEvidence{}, err
			}
		}
		dispatched := dispatch(ctx, node)
		stopObservation()
		// A failed provider dispatch can carry the resolved private node in
		// its receipt even after cancellation or session shutdown. Do not
		// disclose that metadata once the operation loses authority.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(ctxErr, dispatched.err)
		}
		if openErr := l.bundle.session.ensureOpen(); openErr != nil {
			return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(openErr, dispatched.err)
		}
		receipt = dispatched.receipt
		if dispatched.err == nil {
			return l.waitForOutcome(ctx, receipt, postcondition, waitConfig)
		}
		if !locatorRetryAllowed(dispatched, attemptNumber) {
			return receipt, WaitEvidence{}, dispatched.err
		}
		refreshedRoot, refreshedIdentity, scopeErr := l.scopeRoot(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(ctxErr, scopeErr)
		}
		if openErr := l.bundle.session.ensureOpen(); openErr != nil {
			return accessibility.ActionReceipt{}, WaitEvidence{}, errors.Join(openErr, scopeErr)
		}
		if scopeErr != nil {
			return receipt, WaitEvidence{}, scopeErr
		}
		if !sessionaccessibility.SameLocatorScope(identity, refreshedIdentity) || root.BusName != refreshedRoot.BusName || root.ObjectPath != refreshedRoot.ObjectPath {
			// The prior not-sent attempt is not a capability for a different
			// application scope. Suppress its private node metadata.
			return accessibility.ActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s scope changed before dispatch: %w", operation, ErrManagedScopeChanged)
		}
	}
	return receipt, WaitEvidence{}, ErrUnavailable
}

func (l *AccessibilityLocator) waitForOutcome(ctx context.Context, receipt accessibility.ActionReceipt, postcondition Condition, config waitConfig) (accessibility.ActionReceipt, WaitEvidence, error) {
	receipt.Outcome = accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeNotVerified, Condition: postcondition.describe()}
	evidence, err := l.bundle.session.waitWithEvidence(ctx, postcondition, config)
	if err == nil {
		receipt.Outcome.Status = accessibility.ActionOutcomeVerified
		receipt.Outcome.ObservedAt = time.Now().UTC()
	} else {
		err = &ActionVerificationError{Dispatch: receipt.Dispatch, Condition: postcondition.describe(), Err: err}
	}
	return receipt, evidence, err
}

// AccessibilityWaitState names a semantic state that an accessibility
// locator can wait for. All checks use a fresh complete snapshot in the
// locator's existing scope.
type AccessibilityWaitState string

const (
	// AccessibilityWaitPresent succeeds when one control matches the locator.
	AccessibilityWaitPresent AccessibilityWaitState = "present"
	// AccessibilityWaitAbsent succeeds when a complete scoped snapshot has no match.
	AccessibilityWaitAbsent AccessibilityWaitState = "absent"
	// AccessibilityWaitFocused succeeds when the matched node reports focused.
	AccessibilityWaitFocused AccessibilityWaitState = "focused"
	// AccessibilityWaitUnfocused succeeds when the matched node reports unfocused.
	AccessibilityWaitUnfocused AccessibilityWaitState = "unfocused"
	// AccessibilityWaitVisible succeeds when the matched node has AT-SPI VISIBLE.
	AccessibilityWaitVisible AccessibilityWaitState = "visible"
	// AccessibilityWaitHidden succeeds when the matched node lacks AT-SPI VISIBLE.
	AccessibilityWaitHidden AccessibilityWaitState = "hidden"
	// AccessibilityWaitEnabled succeeds when the matched node reports enabled.
	AccessibilityWaitEnabled AccessibilityWaitState = "enabled"
	// AccessibilityWaitDisabled succeeds when the matched node lacks enabled.
	AccessibilityWaitDisabled AccessibilityWaitState = "disabled"
	// AccessibilityWaitValueChanged succeeds when the matched node's accessible value changes.
	AccessibilityWaitValueChanged AccessibilityWaitState = "value-changed"
)

// WaitForState waits for a scoped control state with an explicit positive
// timeout. Value changes use the first complete observation of the uniquely
// matched node as their baseline. WaitEvidence is diagnostic; only a fresh
// snapshot can satisfy the condition. WaitEvery may override the session's
// fallback polling interval.
func (l *AccessibilityLocator) waitForStateContext(
	ctx context.Context,
	state AccessibilityWaitState,
	timeout time.Duration,
	options ...WaitOption,
) (accessibility.Node, WaitEvidence, error) {
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return accessibility.Node{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: nil context", ErrInvalidArgument)
	}
	if !validAccessibilityWaitState(state) {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: unknown state %q", ErrInvalidArgument, state)
	}
	if timeout <= 0 {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: timeout must be positive", ErrInvalidArgument)
	}
	if l.options.VisibleOnly && (state == AccessibilityWaitAbsent || state == AccessibilityWaitHidden) {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: visible-only snapshots cannot prove absence or a hidden state", ErrInvalidArgument)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return l.waitForState(waitCtx, state, options...)
}

func validAccessibilityWaitState(state AccessibilityWaitState) bool {
	switch state {
	case AccessibilityWaitPresent,
		AccessibilityWaitAbsent,
		AccessibilityWaitFocused,
		AccessibilityWaitUnfocused,
		AccessibilityWaitVisible,
		AccessibilityWaitHidden,
		AccessibilityWaitEnabled,
		AccessibilityWaitDisabled,
		AccessibilityWaitValueChanged:
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Scope identity and terminal wait failures stay explicit here.
func (l *AccessibilityLocator) waitForState(
	ctx context.Context,
	state AccessibilityWaitState,
	options ...WaitOption,
) (accessibility.Node, WaitEvidence, error) {
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return accessibility.Node{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: nil context", ErrInvalidArgument)
	}
	if !validAccessibilityWaitState(state) {
		return accessibility.Node{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility wait: %w: unknown state %q", ErrInvalidArgument, state)
	}

	var matched accessibility.Node
	var initialScope sessionaccessibility.LocatorIdentity
	var initialRoot accessibility.NodeID
	haveScope := false
	var targetID accessibility.NodeID
	haveTarget := false
	var baseline *accessibilityWaitValue

	condition := sessionCondition("accessibility locator "+string(state), func(ctx context.Context, _ *sessionCore) (bool, error) {
		var pinnedTarget *accessibility.NodeID
		if haveTarget && state != AccessibilityWaitAbsent {
			pinnedTarget = &targetID
		}
		nodes, scope, root, stopObservation, err := l.findTarget(ctx, pinnedTarget)
		if err != nil {
			if errors.Is(err, accessibility.ErrNotFound) {
				if state == AccessibilityWaitAbsent || haveScope {
					return false, locatorScopeLost(err)
				}
				// An application-scoped presence/state wait may begin before the
				// application is registered. No scope has been established yet, so
				// keep waiting; once observed, its identity is pinned below.
				return false, nil
			}
			if errors.Is(err, accessibility.ErrAmbiguous) || errors.Is(err, accessibility.ErrIncompleteSnapshot) || errors.Is(err, accessibility.ErrScope) || errors.Is(err, ErrManagedScopeChanged) {
				return false, locatorUnverifiable(err)
			}
			if errors.Is(err, accessibility.ErrDisconnected) || errors.Is(err, ErrUnavailable) {
				// A missing semantic bus/capability cannot be repaired by waiting
				// for a control state. Keep its typed cause instead of returning a
				// timeout that hides the observation failure.
				return false, locatorUnverifiable(err)
			}
			return false, err
		}
		defer stopObservation()

		if haveScope && locatorWaitScopeChanged(initialScope, initialRoot, scope, root) {
			return false, locatorScopeLost(fmt.Errorf("locator scope identity changed during wait: %w", ErrManagedScopeChanged))
		}
		if !haveScope {
			initialScope = scope
			initialRoot = root
			haveScope = true
		}

		if state == AccessibilityWaitAbsent {
			switch len(nodes) {
			case 0:
				return true, nil
			case 1:
				return false, nil
			default:
				return false, locatorUnverifiable(accessibility.ErrAmbiguous)
			}
		}
		if len(nodes) > 1 {
			return false, locatorUnverifiable(accessibility.ErrAmbiguous)
		}
		if len(nodes) == 0 {
			if haveTarget {
				return false, locatorTargetChanged(fmt.Errorf("resolved accessibility target disappeared: %w", accessibility.ErrStaleNode))
			}
			return false, nil
		}

		node := nodes[0]
		if !haveTarget {
			targetID = node.ID
			haveTarget = true
		} else if node.ID != targetID {
			return false, locatorTargetChanged(fmt.Errorf("resolved accessibility target identity changed: %w", accessibility.ErrStaleNode))
		}

		satisfied, err := evaluateAccessibilityWaitState(node, state, &baseline)
		if err != nil {
			return false, err
		}
		if satisfied {
			matched = node
		}
		return satisfied, nil
	}, CapabilityAccessibility)

	evidence, err := l.bundle.session.WaitWithEvidence(ctx, condition, options...)
	return matched, evidence, err
}

func locatorUnverifiable(err error) error {
	return errors.Join(errLocatorUnverifiable, err)
}

func locatorScopeLost(err error) error {
	return errors.Join(errLocatorUnverifiable, ErrManagedScopeChanged, err)
}

func locatorTargetChanged(err error) error {
	return errors.Join(errLocatorUnverifiable, ErrAccessibilityTargetChanged, err)
}

func locatorWaitScopeChanged(initial sessionaccessibility.LocatorIdentity, initialRoot accessibility.NodeID, current sessionaccessibility.LocatorIdentity, currentRoot accessibility.NodeID) bool {
	return sessionaccessibility.LocatorIdentityChanged(true, initial, initialRoot, current, currentRoot) ||
		initialRoot.Generation != currentRoot.Generation || initialRoot.Incarnation != currentRoot.Incarnation
}

type accessibilityWaitValue struct {
	kind   string
	text   string
	number float64
}

func evaluateAccessibilityWaitState(node accessibility.Node, state AccessibilityWaitState, baseline **accessibilityWaitValue) (bool, error) {
	switch state {
	case AccessibilityWaitPresent:
		return true, nil
	case AccessibilityWaitFocused, AccessibilityWaitUnfocused, AccessibilityWaitVisible, AccessibilityWaitHidden, AccessibilityWaitEnabled, AccessibilityWaitDisabled:
		if nodeHasWarning(node, "org.a11y.atspi.Accessible.GetState:") {
			return false, locatorUnverifiable(fmt.Errorf("%w: target state set was not observed", accessibility.ErrIncompleteSnapshot))
		}
		switch state {
		case AccessibilityWaitFocused:
			return node.Focused, nil
		case AccessibilityWaitUnfocused:
			return !node.Focused, nil
		case AccessibilityWaitVisible:
			return node.Visible, nil
		case AccessibilityWaitHidden:
			return !node.Visible, nil
		case AccessibilityWaitEnabled:
			return node.Enabled, nil
		case AccessibilityWaitDisabled:
			return !node.Enabled, nil
		}
	case AccessibilityWaitValueChanged:
		current, err := accessibilityWaitValueFor(node)
		if err != nil {
			return false, err
		}
		if *baseline == nil {
			*baseline = &current
			return false, nil
		}
		if current.kind != (*baseline).kind {
			return false, locatorUnverifiable(fmt.Errorf("%w: accessible value interface changed during wait", accessibility.ErrIncompleteSnapshot))
		}
		if current.kind == "number" {
			return current.number != (*baseline).number, nil
		}
		return current.text != (*baseline).text, nil
	}
	return false, fmt.Errorf("perfuncted: accessibility wait: %w: unknown state %q", ErrInvalidArgument, state)
}

func accessibilityWaitValueFor(node accessibility.Node) (accessibilityWaitValue, error) {
	if nodeHasWarning(node, "org.a11y.atspi.Value.CurrentValue:") {
		return accessibilityWaitValue{}, locatorUnverifiable(fmt.Errorf("%w: target value was not observed", accessibility.ErrIncompleteSnapshot))
	}
	if node.Value != nil {
		if math.IsNaN(node.Value.Current) || math.IsInf(node.Value.Current, 0) {
			return accessibilityWaitValue{}, locatorUnverifiable(fmt.Errorf("%w: accessible numeric value is not finite", accessibility.ErrIncompleteSnapshot))
		}
		return accessibilityWaitValue{kind: "number", number: node.Value.Current}, nil
	}
	if hasAccessibilityInterface(node, "org.a11y.atspi.Value") {
		return accessibilityWaitValue{}, accessibilityWaitUnsupported("AT-SPI Value.CurrentValue is unavailable")
	}
	if hasAccessibilityInterface(node, "org.a11y.atspi.Text") {
		if node.Redacted {
			return accessibilityWaitValue{}, accessibilityWaitUnsupported("accessible text is redacted")
		}
		if node.TextTruncated || nodeHasWarning(node, "org.a11y.atspi.Text:") {
			return accessibilityWaitValue{}, locatorUnverifiable(fmt.Errorf("%w: target text value was not observed completely", accessibility.ErrIncompleteSnapshot))
		}
		return accessibilityWaitValue{kind: "text", text: node.Text}, nil
	}
	return accessibilityWaitValue{}, accessibilityWaitUnsupported("target exposes neither AT-SPI Value nor Text")
}

func accessibilityWaitUnsupported(reason string) error {
	return errors.Join(ErrUnsupported, accessibility.ErrUnsupported, fmt.Errorf("accessibility wait: %s", reason))
}

func hasAccessibilityInterface(node accessibility.Node, name string) bool {
	for _, iface := range node.Interfaces {
		if strings.EqualFold(strings.TrimSpace(iface), name) {
			return true
		}
	}
	return false
}

func nodeHasWarning(node accessibility.Node, prefix string) bool {
	for _, warning := range node.Warnings {
		if strings.HasPrefix(strings.TrimSpace(warning), prefix) {
			return true
		}
	}
	return false
}
