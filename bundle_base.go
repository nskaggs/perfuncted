package perfuncted

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

type bundleBase struct {
	session    *Session
	capability Capability

	// Fixed capabilities are resolved before Open returns and never change.
	// This immutable view avoids locking capability metadata on every input,
	// screen and window call. Accessibility deliberately remains dynamic.
	resolvedOperations []string
	operationsResolved bool
}

// backendContext gives each bounded backend operation the session's effective
// policy while retaining an earlier caller deadline or cancellation cause.
// Long-lived event streams intentionally do not use this helper: their
// lifetime belongs to the caller's context.
func (b *bundleBase) backendContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx = contextutil.Default(ctx)
	if b == nil || b.session == nil {
		return ctx, func() {}
	}
	if timeout <= 0 {
		timeout = b.session.Timeouts().Medium
	}
	// A caller deadline already earlier than the backend limit needs no
	// child cancellation context. This is especially useful for frequent
	// screen hash polls under one shared page-readiness deadline.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (b *bundleBase) traceAction(component, format string, args ...any) {
	if b == nil || b.session == nil || b.session.tracer == nil {
		return
	}
	b.session.tracer.Tracef(component, format, args...)
}

func (b *bundleBase) unavailable(operation string) error {
	if b == nil || b.session == nil {
		return ErrNilSession
	}
	err := ErrUnavailable
	if failure := b.session.Capability(b.capability).Failure; failure != nil {
		err = errors.Join(ErrUnavailable, failure)
	}
	return &CapabilityError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}

// cacheOperations binds the immutable status slice rather than cloning it.
// Session owns the data; public Capability returns independent snapshots.
// It must run before the session is published and never after.
func (b *bundleBase) cacheOperations(operations []string) {
	b.resolvedOperations = operations
	b.operationsResolved = true
}

func (b *bundleBase) checkAvailable(operation string, available bool) error {
	if b == nil {
		return b.unavailable(operation)
	}
	if b.session == nil {
		return ErrNilSession
	}
	if b.session.isClosed() {
		return ErrSessionClosed
	}
	if !available {
		return b.unavailable(operation)
	}
	// Non-accessibility backends have a fixed operation contract once the
	// session opens; check the prevalidated list without locking the session.
	// Accessibility can reopen or disconnect and must use the live status.
	// Manually assembled test bundles without a cached list also use live data.
	var supported bool
	if b.operationsResolved && b.capability != CapabilityAccessibility {
		supported = slices.Contains(b.resolvedOperations, operation)
	} else {
		b.session.capabilitiesMu.RLock()
		status := b.session.capabilities[b.capability]
		supported = status.Supports(operation)
		b.session.capabilitiesMu.RUnlock()
	}
	if !supported {
		return &CapabilityError{
			Capability: b.capability,
			Operation:  operation,
			Err:        ErrUnsupported,
		}
	}
	return nil
}

func (b *bundleBase) checkBackend(operation string, backend any) error {
	return b.checkAvailable(operation, !util.IsNil(backend))
}

func closeBackend[T interface{ Close() error }](backend T) error {
	if util.IsNil(backend) {
		return nil
	}
	return backend.Close()
}

func (b *bundleBase) operationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, input.ErrNotSupported) || errors.Is(err, window.ErrNotSupported) || errors.Is(err, accessibility.ErrUnsupported) || errors.Is(err, accessibility.ErrUnsupportedCorrelation) {
		err = errors.Join(ErrUnsupported, err)
	}
	if errors.Is(err, window.ErrInvalidArgument) {
		err = errors.Join(ErrInvalidArgument, err)
	}
	var unsupported capability.UnsupportedError
	if errors.As(err, &unsupported) {
		err = errors.Join(ErrUnsupported, err)
	}
	if errors.Is(err, gnomebridge.ErrUnavailable) || errors.Is(err, clipboard.ErrNoClipboardTool) || errors.Is(err, util.ErrNotAvailable) {
		err = errors.Join(ErrUnavailable, err)
	}
	return &OperationError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}
