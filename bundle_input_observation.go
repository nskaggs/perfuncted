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

// KeyState returns one on-demand key state when the observation backend can
// query it. X11 reports aggregate logical state, not physical device origin.
func (b *InputObservationBundle) KeyState(ctx context.Context, name string) (input.StateObservation, error) {
	if err := input.ValidateKeyStateQuery(name); err != nil {
		return input.StateObservation{}, errors.Join(ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-key-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.KeyState(ctx, name)
	})
}

// LockState returns a supported lock-key state separately from whether that
// key is currently held down.
func (b *InputObservationBundle) LockState(ctx context.Context, name string) (input.StateObservation, error) {
	if err := input.ValidateLockStateQuery(name); err != nil {
		return input.StateObservation{}, errors.Join(ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-lock-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.LockState(ctx, name)
	})
}

// MouseButtonState returns one on-demand mouse-button state when the observer
// can query the X11 core pointer mask.
func (b *InputObservationBundle) MouseButtonState(ctx context.Context, button int) (input.StateObservation, error) {
	if err := input.ValidateMouseButtonStateQuery(button); err != nil {
		return input.StateObservation{}, errors.Join(ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-button-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.MouseButtonState(ctx, button)
	})
}

func (b *InputObservationBundle) queryState(
	ctx context.Context,
	operation string,
	query func(input.StateReader, context.Context) (input.StateObservation, error),
) (input.StateObservation, error) {
	if ctx == nil {
		return input.StateObservation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return input.StateObservation{}, err
	}
	if b == nil {
		return input.StateObservation{}, (&bundleBase{}).unavailable(operation)
	}
	if err := b.checkBackend(operation, b.backend); err != nil {
		return input.StateObservation{}, err
	}
	reader, ok := b.backend.(input.StateReader)
	if !ok {
		return input.StateObservation{}, b.operationError(operation, input.ErrObservationUnsupported)
	}
	opCtx, timeoutCancel := b.backendContext(ctx, b.session.Timeouts().Short)
	opCtx, cancel := context.WithCancel(opCtx)
	defer timeoutCancel()
	defer cancel()
	stopSessionCancel := context.AfterFunc(b.session.ctx, cancel) //nolint:contextcheck // Session.Close revokes an in-flight input-state observation.
	defer stopSessionCancel()
	state, err := query(reader, opCtx)
	if ctxErr := opCtx.Err(); ctxErr != nil {
		return input.StateObservation{}, ctxErr
	}
	if openErr := b.session.ensureOpen(); openErr != nil {
		return input.StateObservation{}, openErr
	}
	if err != nil {
		return input.StateObservation{}, b.operationError(operation, err)
	}
	return state, nil
}
