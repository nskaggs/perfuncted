package perfuncted

import (
	"context"
	"errors"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

const inputReceiptHistoryLimit = 32

// InputActionReceipt records a bounded, payload-free result for one input
// operation. Dispatch is unknown when a backend returns an error because the
// operation may have reached the desktop before the error was observed.
type InputActionReceipt struct {
	Sequence     uint64    `json:"sequence"`
	Operation    string    `json:"operation"`
	Mechanism    string    `json:"mechanism,omitempty"`
	Dispatch     string    `json:"dispatch"`
	FailureClass string    `json:"failure_class,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
}

func (b *InputBundle) recordInputReceipt(operation string, started time.Time, err error) error {
	if b == nil {
		return err
	}
	receipt := InputActionReceipt{
		Operation:   operation,
		Dispatch:    inputDispatchResult(err),
		StartedAt:   started.UTC(),
		CompletedAt: time.Now().UTC(),
	}
	if b.session != nil {
		receipt.Mechanism = b.session.Capability(CapabilityInput).Backend
	}
	if err != nil {
		receipt.FailureClass = inputFailureClass(err)
	}

	b.receiptMu.Lock()
	b.receiptSeq++
	receipt.Sequence = b.receiptSeq
	if len(b.receiptTrail) == inputReceiptHistoryLimit {
		copy(b.receiptTrail, b.receiptTrail[1:])
		b.receiptTrail[len(b.receiptTrail)-1] = receipt
	} else {
		b.receiptTrail = append(b.receiptTrail, receipt)
	}
	b.receiptMu.Unlock()
	return err
}

func (b *InputBundle) recentInputReceipts() []InputActionReceipt {
	if b == nil {
		return nil
	}
	b.receiptMu.Lock()
	defer b.receiptMu.Unlock()
	return append([]InputActionReceipt(nil), b.receiptTrail...)
}

func inputDispatchResult(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrUnsupported), errors.Is(err, input.ErrNotSupported):
		return "not-sent"
	default:
		return "unknown"
	}
}

func inputFailureClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline-exceeded"
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrUnsupported), errors.Is(err, input.ErrNotSupported):
		return "rejected-before-dispatch"
	default:
		return "operation-failed"
	}
}
