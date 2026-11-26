package sessioninput

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"time"

	"github.com/nskaggs/perfuncted/v2/input"
)

// InputObservationBundle provides explicit, read-only event subscriptions.
// It never opens or uses the injection backend.
type observationState struct {
	backend input.Observer
	gate    ObservationAdmission
}

func (b *observationState) close() error {
	if b == nil || b.gate == nil {
		return nil
	}
	if util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

// Listen subscribes to a named shortcut or mouse button. The returned
// subscription is bounded and ends on cancellation, overflow, revocation,
// backend failure, or Session.Close.
func (b *observationState) Listen(ctx context.Context, f input.ObservationFilter) (input.ObservationSubscription, error) {
	if ctx == nil {
		return nil, errors.Join(capability.ErrInvalidArgument, input.ErrInvalidObservationFilter)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operation := "listen-hotkey"
	if f.Button != 0 {
		operation = "listen-button"
	}
	if b == nil || b.gate == nil {
		return nil, capability.ErrNilSession
	}
	if err := b.gate.CheckBackend(operation, b.backend); err != nil {
		return nil, err
	}
	subscription, err := b.backend.Listen(ctx, f)
	if err != nil {
		return nil, b.gate.OperationError(operation, err)
	}
	if err := ctx.Err(); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	return subscription, nil
}

// KeyState returns one on-demand key state when the observation backend can
// query it. X11 reports aggregate logical state, not physical device origin.
func (b *observationState) KeyState(ctx context.Context, name string) (input.StateObservation, error) {
	if err := input.ValidateKeyStateQuery(name); err != nil {
		return input.StateObservation{}, errors.Join(capability.ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-key-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.KeyState(ctx, name)
	})
}

// LockState returns a supported lock-key state separately from whether that
// key is currently held down.
func (b *observationState) LockState(ctx context.Context, name string) (input.StateObservation, error) {
	if err := input.ValidateLockStateQuery(name); err != nil {
		return input.StateObservation{}, errors.Join(capability.ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-lock-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.LockState(ctx, name)
	})
}

// MouseButtonState returns one on-demand mouse-button state when the observer
// can query the X11 core pointer mask.
func (b *observationState) MouseButtonState(ctx context.Context, button int) (input.StateObservation, error) {
	if err := input.ValidateMouseButtonStateQuery(button); err != nil {
		return input.StateObservation{}, errors.Join(capability.ErrInvalidArgument, err)
	}
	return b.queryState(ctx, "query-button-state", func(reader input.StateReader, ctx context.Context) (input.StateObservation, error) {
		return reader.MouseButtonState(ctx, button)
	})
}

func (b *observationState) queryState(
	ctx context.Context,
	operation string,
	query func(input.StateReader, context.Context) (input.StateObservation, error),
) (input.StateObservation, error) {
	if ctx == nil {
		return input.StateObservation{}, capability.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return input.StateObservation{}, err
	}
	if b == nil || b.gate == nil {
		return input.StateObservation{}, capability.ErrNilSession
	}
	if err := b.gate.CheckBackend(operation, b.backend); err != nil {
		return input.StateObservation{}, err
	}
	reader, ok := b.backend.(input.StateReader)
	if !ok {
		return input.StateObservation{}, b.gate.OperationError(operation, input.ErrObservationUnsupported)
	}
	opCtx, timeoutCancel := b.gate.InputContext(ctx, 0)
	opCtx, cancel := context.WithCancel(opCtx)
	defer timeoutCancel()
	defer cancel()
	stopSessionCancel := b.gate.AfterClose(cancel) //nolint:contextcheck // Session.Close revokes an in-flight input-state observation.
	defer stopSessionCancel()
	state, err := query(reader, opCtx)
	if ctxErr := opCtx.Err(); ctxErr != nil {
		return input.StateObservation{}, ctxErr
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return input.StateObservation{}, openErr
	}
	if err != nil {
		return input.StateObservation{}, b.gate.OperationError(operation, err)
	}
	return state, nil
}

// ObservationAdmission excludes injection backends and receipt state.
type ObservationAdmission interface {
	capability.BackendAuthority
	InputContext(context.Context, time.Duration) (context.Context, context.CancelFunc)
	AfterClose(func()) func() bool
}
type ObservationOwner struct{ *observationState }
type ObservationHandle struct{ *observationState }

func NewObservation(gate ObservationAdmission, backend input.Observer) *ObservationOwner {
	return &ObservationOwner{&observationState{gate: gate, backend: backend}}
}
func (o *ObservationOwner) Handle() ObservationHandle {
	if o == nil {
		return ObservationHandle{}
	}
	return ObservationHandle{o.observationState}
}
func (o *ObservationOwner) Install(backend input.Observer) { o.backend = backend }
func (o *ObservationOwner) Close() error {
	if o == nil {
		return nil
	}
	return o.close()
}
