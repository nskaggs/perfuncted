package perfuncted

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
	if b == nil {
		return &CapabilityError{
			Operation: operation,
			Err:       ErrNotAvailable,
		}
	}
	err := ErrNotAvailable
	if b.session != nil {
		if failure := b.session.Capability(b.capability).Failure; failure != nil {
			err = failure
		}
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
	if b.session != nil && b.session.isClosed() {
		return ErrSessionClosed
	}
	if !available {
		return b.unavailable(operation)
	}
	return nil
}
