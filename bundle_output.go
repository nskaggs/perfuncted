package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/output"
)

type OutputBundle struct {
	output.Lister
	bundleBase
}

func (b *OutputBundle) List(ctx context.Context) ([]output.Info, error) {
	if b.Lister == nil {
		return nil, &CapabilityError{Cap: CapabilityOutputs, Err: ErrNotAvailable}
	}
	b.traceAction("output", "list")
	return b.Lister.List(ctx)
}

func (b *OutputBundle) close() error {
	if b.Lister == nil {
		return nil
	}
	return b.Lister.Close()
}
