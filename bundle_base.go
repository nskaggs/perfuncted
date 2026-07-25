package perfuncted

type bundleBase struct {
	capability Capability
	failure    error
	tracer     *actionTracer
}

func (b *bundleBase) traceAction(component, format string, args ...any) {
	if b == nil || b.tracer == nil {
		return
	}
	b.tracer.Tracef(component, format, args...)
}

func (b *bundleBase) unavailable(operation string) error {
	if b == nil {
		return &CapabilityError{
			Operation: operation,
			Err:       ErrNotAvailable,
		}
	}
	err := b.failure
	if err == nil {
		err = ErrNotAvailable
	}
	return &CapabilityError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}
