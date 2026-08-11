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
	if b == nil {
		return nil, (&bundleBase{}).unavailable("list")
	}
	if err := b.checkAvailable("list", b.backend != nil); err != nil {
		return nil, err
	}
	b.traceAction("output", "list")
	items, err := b.backend.List(ctx)
	return items, b.operationError("list", err)
}

func (b *OutputBundle) close() error {
	if b == nil || b.backend == nil {
		return nil
	}
	return b.backend.Close()
}
