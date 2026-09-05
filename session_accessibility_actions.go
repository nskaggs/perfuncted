package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
	if ctx == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility action and wait: %w: nil context", ErrInvalidArgument)
	}
	if postcondition == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, fmt.Errorf("perfuncted: accessibility action and wait: %w: nil postcondition", ErrInvalidArgument)
	}
	if s.Accessibility == nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, ErrUnavailable
	}
	receipt, err := s.Accessibility.InvokeSemanticAction(ctx, root, query, actionName, snapshotOptions)
	if err != nil {
		return AccessibilityActionReceipt{}, WaitEvidence{}, err
	}
	evidence, err := s.WaitWithEvidence(ctx, postcondition, waitOptions...)
	return receipt, evidence, err
}
