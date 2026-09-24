package perfuncted

import (
	"context"
	"errors"
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
	if !b.session.Capability(b.capability).Supports(operation) {
		return &CapabilityError{
			Capability: b.capability,
			Operation:  operation,
			Err:        ErrUnsupported,
		}
	}
	return nil
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
