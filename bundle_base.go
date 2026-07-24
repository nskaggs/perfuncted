package perfuncted

import "fmt"

type bundleBase struct {
	tracer *actionTracer
}

func (b *bundleBase) traceAction(component, format string, args ...any) {
	if b == nil || b.tracer == nil {
		return
	}
	b.tracer.Tracef(component, format, args...)
}

func checkAvailable(resource any, name string) error {
	if resource == nil {
		return fmt.Errorf("%s: not available", name)
	}
	return nil
}
