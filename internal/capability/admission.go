package capability

import (
	"context"
	"errors"
)

// ErrNilSession identifies a capability with no session owner.
var ErrNilSession = errors.New("session is nil")
var ErrSessionClosed = errors.New("session is closed")
var ErrUnavailable = errors.New("backend unavailable")
var ErrUnsupported = errors.New("operation unsupported")
var ErrInvalidArgument = errors.New("invalid argument")

// Admission is the narrow, module-private boundary for fixed capability
// operations. The session owns the policy and authority; semantic owners
// own the backend and apply the checks around actual dispatch.
type Admission interface {
	CheckBackend(operation string, backend any) error
	BackendContext(context.Context) (context.Context, context.CancelFunc)
	Trace(component, operation string)
	EnsureOpen() error
	OperationError(operation string, err error) error
}

// Read performs a bounded read and suppresses results after authority loss.
// Backend operation errors remain distinguishable from cancellation.
func Read[T any](ctx context.Context, gate Admission, component, operation string, read func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, cancel := gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	gate.Trace(component, operation)
	value, err := read(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, ctxErr
	}
	if openErr := gate.EnsureOpen(); openErr != nil {
		return zero, openErr
	}
	return value, gate.OperationError(operation, err)
}

// Write preserves the distinction between failed dispatch and late authority
// loss. It does not retry a mutation.
func Write(ctx context.Context, gate Admission, component, operation string, write func(context.Context) error) error {
	ctx, cancel := gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.Trace(component, operation)
	err := write(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, gate.OperationError(operation, err))
	}
	if openErr := gate.EnsureOpen(); openErr != nil {
		return errors.Join(openErr, gate.OperationError(operation, err))
	}
	return gate.OperationError(operation, err)
}
