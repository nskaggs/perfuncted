package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
func (l *AccessibilityLocator) WaitForState(
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
	var initialScope locatorIdentity
	var initialRoot accessibility.NodeID
	haveScope := false
	var targetID accessibility.NodeID
	haveTarget := false
	var baseline *accessibilityWaitValue

	condition := sessionCondition("accessibility locator "+string(state), func(ctx context.Context, _ *Session) (bool, error) {
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

func locatorWaitScopeChanged(initial locatorIdentity, initialRoot accessibility.NodeID, current locatorIdentity, currentRoot accessibility.NodeID) bool {
	return locatorIdentityChanged(true, initial, initialRoot, current, currentRoot) ||
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
