package perfuncted

import (
	"context"
	"errors"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/window"
)

type bundleBase struct {
	session    *Session
	capability Capability
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
	if errors.Is(err, input.ErrNotSupported) || errors.Is(err, window.ErrNotSupported) || errors.Is(err, accessibility.ErrUnsupported) {
		err = errors.Join(ErrUnsupported, err)
	}
	return &OperationError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}
