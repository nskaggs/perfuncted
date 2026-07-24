package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/output"
)

type OutputBundle struct {
	backend output.Lister
	bundleBase
}

func (b *OutputBundle) List(ctx context.Context) ([]output.Info, error) {
	if b == nil || b.backend == nil {
		return nil, b.unavailable("list")
	}
	b.traceAction("output", "list")
	return b.backend.List(ctx)
}

func (b *OutputBundle) close() error {
	if b == nil || b.backend == nil {
		return nil
	}
	return b.backend.Close()
}
