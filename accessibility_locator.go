package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/accessibility"
)

const locatorResolutionAttempts = 2

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

type locatorIdentity struct {
	windowID string
	pid      int32
	appID    string
	busName  string
	name     string
}

// AccessibilityLocator is a lazy semantic target scoped to a managed window
// or a uniquely identified application. It stores selector intent and resolves
// provider nodes only when an operation runs. Resolution requires an active
// AT-SPI event stream so provider changes during a fresh snapshot can fail closed.
type AccessibilityLocator struct {
	bundle   *AccessibilityBundle
	scope    locatorScope
	selector accessibility.Selector
	options  accessibility.SnapshotOptions
}

// LocatorForWindow creates a locator rooted in a managed Perfuncted window.
// The window and AT-SPI scope are re-resolved for each operation.
func (b *AccessibilityBundle) LocatorForWindow(windowID string, selector accessibility.Selector, options accessibility.SnapshotOptions) *AccessibilityLocator {
	return &AccessibilityLocator{
		bundle: b, scope: locatorScope{kind: locatorWindowScope, windowID: strings.TrimSpace(windowID)},
		selector: cloneSelector(selector), options: options,
	}
}

// LocatorForApplication creates a locator rooted in an application selected
// by authoritative accessibility identity. Ambiguous application filters fail
// before selector matching.
func (b *AccessibilityBundle) LocatorForApplication(filter accessibility.ApplicationFilter, selector accessibility.Selector, options accessibility.SnapshotOptions) *AccessibilityLocator {
	return &AccessibilityLocator{
		bundle: b, scope: locatorScope{kind: locatorApplicationScope, application: filter},
		selector: cloneSelector(selector), options: options,
	}
}

func cloneSelector(selector accessibility.Selector) accessibility.Selector {
	selector.States = append([]string(nil), selector.States...)
	selector.Ancestors = append([]accessibility.AncestorSelector(nil), selector.Ancestors...)
	if selector.Attributes != nil {
		attributes := make(map[string]string, len(selector.Attributes))
		for key, value := range selector.Attributes {
			attributes[key] = value
		}
		selector.Attributes = attributes
	}
	return selector
}

func (l *AccessibilityLocator) scopeRoot(ctx context.Context) (accessibility.NodeID, locatorIdentity, error) {
	if l == nil || l.bundle == nil {
		return accessibility.NodeID{}, locatorIdentity{}, ErrUnavailable
	}
	switch l.scope.kind {
	case locatorWindowScope:
		if l.scope.windowID == "" {
			return accessibility.NodeID{}, locatorIdentity{}, fmt.Errorf("perfuncted: %w: locator requires a managed window ID", ErrInvalidArgument)
		}
		scope, err := l.bundle.WindowRootFresh(ctx, l.scope.windowID)
		if err != nil {
			return accessibility.NodeID{}, locatorIdentity{}, err
		}
		return scope.Root, locatorIdentity{
			windowID: scope.WindowID, pid: scope.PID, appID: scope.AppID,
			busName: scope.Application.ID.BusName, name: scope.Application.Name,
		}, nil
	case locatorApplicationScope:
		app, err := l.bundle.FindApplicationFresh(ctx, l.scope.application)
		if err != nil {
			return accessibility.NodeID{}, locatorIdentity{}, err
		}
		return app.ID, locatorIdentity{pid: app.PID, busName: app.ID.BusName, name: app.Name}, nil
	default:
		return accessibility.NodeID{}, locatorIdentity{}, fmt.Errorf("perfuncted: %w: locator scope is missing", ErrInvalidArgument)
	}
}

func sameLocatorScope(a, b locatorIdentity) bool {
	if a.windowID != "" || b.windowID != "" {
		if a.windowID != b.windowID {
			return false
		}
	}
	if a.busName != "" && b.busName != "" && a.busName != b.busName {
		return false
	}
	if a.pid != 0 && b.pid != 0 {
		return a.pid == b.pid && sameOptionalIdentity(a.appID, b.appID)
	}
	if a.appID != "" && b.appID != "" {
		return a.appID == b.appID
	}
	if a.busName != "" && b.busName != "" {
		return a.busName == b.busName
	}
	return a.name != "" && strings.EqualFold(strings.TrimSpace(a.name), strings.TrimSpace(b.name))
}

func sameOptionalIdentity(a, b string) bool {
	return a == "" || b == "" || a == b
}

func locatorIdentityChanged(have bool, initial locatorIdentity, initialRoot accessibility.NodeID, current locatorIdentity, currentRoot accessibility.NodeID) bool {
	if !have {
		return false
	}
	return !sameLocatorScope(initial, current) || initialRoot.BusName != currentRoot.BusName || initialRoot.ObjectPath != currentRoot.ObjectPath
}

func locatorResolutionMayRetry(err error) bool {
	return errors.Is(err, accessibility.ErrStaleGeneration) ||
		errors.Is(err, accessibility.ErrStaleNode) ||
		errors.Is(err, accessibility.ErrObservationChanged)
}

func (l *AccessibilityLocator) find(ctx context.Context) ([]accessibility.Node, locatorIdentity, accessibility.NodeID, context.CancelFunc, error) {
	if l == nil || l.bundle == nil {
		return nil, locatorIdentity{}, accessibility.NodeID{}, nil, ErrUnavailable
	}
	if ctx == nil {
		return nil, locatorIdentity{}, accessibility.NodeID{}, nil, fmt.Errorf("perfuncted: locator: %w: nil context", ErrInvalidArgument)
	}
	if err := l.bundle.checkAvailable("find"); err != nil {
		return nil, locatorIdentity{}, accessibility.NodeID{}, nil, err
	}
	var initialIdentity locatorIdentity
	var initialRoot accessibility.NodeID
	haveInitialIdentity := false
	for attempt := 0; attempt < locatorResolutionAttempts; attempt++ {
		observationCtx, stopObservation := context.WithCancel(ctx)
		if _, err := l.bundle.Events(observationCtx, accessibility.EventOptions{Buffer: 1, RequireSemanticCoverage: true}); err != nil {
			stopObservation()
			return nil, locatorIdentity{}, accessibility.NodeID{}, nil, l.bundle.operationError("find", fmt.Errorf("%w: locator event observation is unavailable: %w", accessibility.ErrObservationChanged, err))
		}
		root, identity, err := l.scopeRoot(ctx)
		if err == nil {
			if locatorIdentityChanged(haveInitialIdentity, initialIdentity, initialRoot, identity, root) {
				stopObservation()
				return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", fmt.Errorf("%w: locator scope identity changed during resolution", accessibility.ErrScope))
			}
			if !haveInitialIdentity {
				initialIdentity = identity
				initialRoot = root
				haveInitialIdentity = true
			}
			opts := l.options
			opts.Fresh = true
			ctxForRead, cancel := l.bundle.operationContext(ctx)
			snapshot, readErr := l.bundle.backend.Snapshot(ctxForRead, root, opts)
			cancel()
			if readErr == nil {
				if incomplete := accessibility.ValidateSemanticSnapshot(snapshot, l.selector); incomplete != nil {
					stopObservation()
					return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", incomplete)
				}
				return accessibility.FilterSnapshotSelector(snapshot, l.selector), identity, root, stopObservation, nil
			}
			err = readErr
		}
		if !locatorResolutionMayRetry(err) {
			stopObservation()
			return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", err)
		}
		if attempt+1 == locatorResolutionAttempts {
			stopObservation()
			return nil, identity, accessibility.NodeID{}, nil, l.bundle.operationError("find", err)
		}
		stopObservation()
	}
	return nil, locatorIdentity{}, accessibility.NodeID{}, nil, accessibility.ErrStaleGeneration
}

// Matches returns all matches from one complete, current snapshot. It preserves
// ambiguity for discovery callers; Resolve applies the unique-target rule.
func (l *AccessibilityLocator) Matches(ctx context.Context) ([]accessibility.Node, error) {
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
func (l *AccessibilityLocator) Resolve(ctx context.Context) (accessibility.Node, error) {
	nodes, err := l.Matches(ctx)
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
func (l *AccessibilityLocator) Wait(ctx context.Context, options ...WaitOption) (accessibility.Node, WaitEvidence, error) {
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return accessibility.Node{}, WaitEvidence{}, ErrUnavailable
	}
	var matched accessibility.Node
	condition := sessionCondition("accessibility locator", func(ctx context.Context, _ *Session) (bool, error) {
		node, err := l.Resolve(ctx)
		if errors.Is(err, accessibility.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		matched = node
		return true, nil
	})
	evidence, err := l.bundle.session.WaitWithEvidence(ctx, condition, options...)
	return matched, evidence, err
}

// InvokeActionAndWait resolves one current target, dispatches one semantic
// action, and then waits for an independent caller-supplied postcondition.
func (l *AccessibilityLocator) InvokeActionAndWait(ctx context.Context, actionName string, postcondition Condition, options ...WaitOption) (AccessibilityActionReceipt, WaitEvidence, error) { //nolint:gocyclo // unique resolution, dispatch certainty, and bounded stale handling are explicit safety gates.
	return l.actionAndWait(ctx, "action", postcondition, options, func(ctx context.Context, node accessibility.Node) locatorDispatchAttempt {
		receipt, err := l.bundle.invokeSemanticActionOnNode(ctx, node, actionName)
		retryable := err != nil && locatorActionMayRetryStale(err) &&
			(receipt.Dispatch == "" || receipt.Dispatch == accessibility.DispatchNotSent)
		return locatorDispatchAttempt{receipt: receipt, err: err, retryableNotSent: retryable}
	})
}

// ClickAndWait invokes the target's accessible default action once and records
// whether an independent caller-supplied postcondition becomes true.
func (l *AccessibilityLocator) ClickAndWait(ctx context.Context, postcondition Condition, options ...WaitOption) (AccessibilityActionReceipt, WaitEvidence, error) {
	return l.InvokeActionAndWait(ctx, "", postcondition, options...)
}

// FillAndWait replaces editable text on the uniquely resolved target with one
// AT-SPI SetTextContents dispatch, then observes an independent postcondition.
// An uncertain provider result is returned with its receipt and is never retried.
func (l *AccessibilityLocator) FillAndWait(ctx context.Context, value string, postcondition Condition, options ...WaitOption) (AccessibilityActionReceipt, WaitEvidence, error) { //nolint:gocyclo // one text mutation, bounded stale handling, and independent outcome proof are explicit gates.
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: nil context", ErrInvalidArgument)
	}
	if postcondition == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: nil postcondition", ErrInvalidArgument)
	}
	if !utf8.ValidString(value) {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator fill: %w: text must be valid UTF-8", ErrInvalidArgument)
	}
	return l.actionAndWait(ctx, "fill", postcondition, options, func(ctx context.Context, node accessibility.Node) locatorDispatchAttempt {
		receipt := AccessibilityActionReceipt{
			Node: node, Operation: "set-text-contents", Mechanism: "at-spi.editable-text",
			Generation: node.ID.Generation, Dispatch: accessibility.DispatchNotSent,
			Outcome: ActionOutcomeProof{Status: ActionOutcomeNotObserved},
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
		receipt.Dispatch = textDispatchOutcome(err)
		return locatorDispatchAttempt{
			receipt: receipt, err: err,
			retryableNotSent: receipt.Dispatch == accessibility.DispatchNotSent && locatorActionMayRetryStale(err),
		}
	})
}

type locatorDispatchAttempt struct {
	receipt          AccessibilityActionReceipt
	err              error
	retryableNotSent bool
}

func locatorRetryAllowed(attempt locatorDispatchAttempt, attemptNumber int) bool {
	return attempt.err != nil && attempt.retryableNotSent && locatorActionMayRetryStale(attempt.err) && attemptNumber+1 < locatorResolutionAttempts
}

func locatorActionMayRetryStale(err error) bool {
	return errors.Is(err, accessibility.ErrStaleGeneration) || errors.Is(err, accessibility.ErrStaleNode)
}

func (l *AccessibilityLocator) actionAndWait(ctx context.Context, operation string, postcondition Condition, options []WaitOption, dispatch func(context.Context, accessibility.Node) locatorDispatchAttempt) (AccessibilityActionReceipt, WaitEvidence, error) {
	if l == nil || l.bundle == nil || l.bundle.session == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, ErrUnavailable
	}
	if ctx == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s: %w: nil context", operation, ErrInvalidArgument)
	}
	if postcondition == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s: %w: nil postcondition", operation, ErrInvalidArgument)
	}
	var receipt AccessibilityActionReceipt
	for attemptNumber := 0; attemptNumber < locatorResolutionAttempts; attemptNumber++ {
		matches, identity, root, stopObservation, err := l.find(ctx)
		if err != nil {
			return receipt, WaitEvidence{}, err
		}
		node, err := uniqueLocatorMatch(matches)
		if err != nil {
			stopObservation()
			return receipt, WaitEvidence{}, err
		}
		dispatched := dispatch(ctx, node)
		stopObservation()
		receipt = dispatched.receipt
		if dispatched.err == nil {
			return l.waitForOutcome(ctx, receipt, postcondition, options...)
		}
		if !locatorRetryAllowed(dispatched, attemptNumber) {
			return receipt, WaitEvidence{}, dispatched.err
		}
		refreshedRoot, refreshedIdentity, scopeErr := l.scopeRoot(ctx)
		if scopeErr != nil {
			return receipt, WaitEvidence{}, scopeErr
		}
		if !sameLocatorScope(identity, refreshedIdentity) || root.BusName != refreshedRoot.BusName || root.ObjectPath != refreshedRoot.ObjectPath {
			return receipt, WaitEvidence{}, fmt.Errorf("perfuncted: locator %s scope changed before dispatch: %w", operation, ErrManagedScopeChanged)
		}
	}
	return receipt, WaitEvidence{}, ErrUnavailable
}

func textDispatchOutcome(err error) accessibility.DispatchOutcome {
	switch {
	case err == nil:
		return accessibility.DispatchAccepted
	case errors.Is(err, accessibility.ErrStaleGeneration), errors.Is(err, accessibility.ErrStaleNode),
		errors.Is(err, accessibility.ErrUnsupported), errors.Is(err, accessibility.ErrMutationRejected):
		if errors.Is(err, accessibility.ErrMutationRejected) {
			return accessibility.DispatchRejected
		}
		return accessibility.DispatchNotSent
	default:
		return accessibility.DispatchUnknown
	}
}

func (l *AccessibilityLocator) waitForOutcome(ctx context.Context, receipt AccessibilityActionReceipt, postcondition Condition, options ...WaitOption) (AccessibilityActionReceipt, WaitEvidence, error) {
	receipt.Outcome = ActionOutcomeProof{Status: ActionOutcomeNotVerified, Condition: postcondition.describe()}
	evidence, err := l.bundle.session.WaitWithEvidence(ctx, postcondition, options...)
	if err == nil {
		receipt.Outcome.Status = ActionOutcomeVerified
		receipt.Outcome.ObservedAt = time.Now().UTC()
	}
	return receipt, evidence, err
}

func uniqueLocatorMatch(nodes []accessibility.Node) (accessibility.Node, error) {
	switch len(nodes) {
	case 0:
		return accessibility.Node{}, accessibility.ErrNotFound
	case 1:
		return nodes[0], nil
	default:
		return accessibility.Node{}, accessibility.ErrAmbiguous
	}
}
