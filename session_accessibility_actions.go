package perfuncted

import (
	"context"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
func (s *Session) InvokeAccessibilityActionAndWait(
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
	if s.Accessibility == nil || !s.Has(CapabilityAccessibility) {
		return AccessibilityActionReceipt{}, WaitEvidence{}, &CapabilityError{
			Capability: CapabilityAccessibility,
			Operation:  "invoke-action-and-wait",
			Err:        ErrUnavailable,
		}
	}
	var receipt AccessibilityActionReceipt
	if targetCondition, ok := postcondition.(actionTargetCondition); ok {
		var node accessibility.Node
		node, err = s.Accessibility.FindOne(ctx, root, query, snapshotOptions)
		if err == nil {
			err = targetCondition.validateActionTarget(root, node)
		}
		if err == nil {
			receipt, err = s.Accessibility.invokeSemanticActionOnNode(ctx, node, actionName)
		}
	} else {
		receipt, err = s.Accessibility.InvokeSemanticAction(ctx, root, query, actionName, snapshotOptions)
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
