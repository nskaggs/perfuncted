package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/output"
)

// OutputBundle wraps read-only output listing.
type OutputBundle struct {
	output.Lister
	tracer *actionTracer
}

func (o OutputBundle) close() error {
	if o.Lister == nil {
		return nil
	}
	o.traceAction("close")
	return o.Lister.Close()
}

func (o OutputBundle) checkAvailable() error {
	return util.CheckAvailable("output", o.Lister)
}

func (o OutputBundle) traceAction(msg string) {
	if o.tracer == nil {
		return
	}
	o.tracer.Tracef("output", "%s", msg)
}

func (o OutputBundle) List(ctx context.Context) ([]output.Info, error) {
	o.traceAction("list")
	if err := o.checkAvailable(); err != nil {
		return nil, err
	}
	return o.Lister.List(ctx)
}

func (o OutputBundle) String() string {
	if o.Lister == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", o.Lister)
}
