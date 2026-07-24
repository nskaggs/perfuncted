package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/output"
)

type OutputBundle struct {
	output.Lister
	bundleBase
}

func (o OutputBundle) close() error {
	if o.Lister == nil {
		return nil
	}
	o.traceAction("output", "close")
	return o.Lister.Close()
}

func (o OutputBundle) checkAvailable() error {
	return checkAvailable(o.Lister, "output")
}

func (o OutputBundle) List(ctx context.Context) ([]output.Info, error) {
	o.traceAction("output", "list")
	if err := o.checkAvailable(); err != nil {
		return nil, err
	}
	return o.Lister.List(ctx)
}
