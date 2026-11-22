package perfuncted

import (
	"context"
	"errors"

	"github.com/nskaggs/perfuncted/input"
)

// InputObservationBundle provides explicit, read-only event subscriptions.
// It never opens or uses the injection backend.
type InputObservationBundle struct {
	backend input.Observer
	bundleBase
}

func (b *InputObservationBundle) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}

// Listen subscribes to a named shortcut or mouse button. The returned
// subscription is bounded and ends on cancellation, overflow, revocation,
// backend failure, or Session.Close.
func (b *InputObservationBundle) Listen(ctx context.Context, f input.ObservationFilter) (input.ObservationSubscription, error) {
	if ctx == nil {
		return nil, errors.Join(ErrInvalidArgument, input.ErrInvalidObservationFilter)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operation := "listen-hotkey"
	if f.Button != 0 {
		operation = "listen-button"
	}
	if b == nil {
		return nil, (&bundleBase{}).unavailable(operation)
	}
	if err := b.checkBackend(operation, b.backend); err != nil {
		return nil, err
	}
	subscription, err := b.backend.Listen(ctx, f)
	if err != nil {
		return nil, b.operationError(operation, err)
	}
	if err := ctx.Err(); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	if err := b.session.ensureOpen(); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	return subscription, nil
}
