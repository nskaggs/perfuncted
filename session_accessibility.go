package perfuncted

import (
	"context"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/sessionaccessibility"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"strings"
	"time"
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
	windows, err := b.session.windows.List(ctx, WindowMatch{})
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

// AccessibilityActionReceipt records semantic dispatch and independent observation.
type AccessibilityActionReceipt = accessibility.ActionReceipt

// ActionOutcomeStatus identifies the outcome of independent observation.
type ActionOutcomeStatus = accessibility.ActionOutcomeStatus

// ActionOutcomeProof records a verified or unverified postcondition.
type ActionOutcomeProof = accessibility.ActionOutcomeProof

// Semantic action outcome statuses.
const (
	ActionOutcomeNotObserved = accessibility.ActionOutcomeNotObserved
	ActionOutcomeVerified    = accessibility.ActionOutcomeVerified
	ActionOutcomeNotVerified = accessibility.ActionOutcomeNotVerified
)

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
func (b *accessibilityState) invokeSemanticActionOnNode(ctx context.Context, node accessibility.Node, name string) (AccessibilityActionReceipt, error) {
	if b == nil {
		return AccessibilityActionReceipt{}, ErrNilSession
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
) (AccessibilityActionReceipt, WaitEvidence, error) {
	if s == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, ErrNilSession
	}
	config, err := s.prepareWait(ctx, postcondition, waitOptions, "accessibility-action-verification")
	if err != nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, err
	}
	if s.accessibility == nil || !s.Has(CapabilityAccessibility) {
		return AccessibilityActionReceipt{}, WaitEvidence{}, &CapabilityError{
			Capability: CapabilityAccessibility,
			Operation:  "invoke-action-and-wait",
			Err:        ErrUnavailable,
		}
	}
	var receipt AccessibilityActionReceipt
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
	receipt.Outcome = ActionOutcomeProof{Status: ActionOutcomeNotVerified, Condition: postcondition.describe()}
	if err == nil {
		receipt.Outcome.Status = ActionOutcomeVerified
		receipt.Outcome.ObservedAt = time.Now().UTC()
	} else {
		err = &ActionVerificationError{Dispatch: receipt.Dispatch, Condition: postcondition.describe(), Err: err}
	}
	return receipt, evidence, err
}
