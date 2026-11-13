package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/output"
)

// OutputBundle exposes display-output operations through a Session.
type OutputBundle struct {
	backend output.Lister
	bundleBase
}

func (b *OutputBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.checkBackend(operation, b.backend)
}

// List returns the displays visible to the active output backend.
func (b *OutputBundle) List(ctx context.Context) ([]output.Info, error) {
	if b == nil {
		return nil, (&bundleBase{}).unavailable("list")
	}
	if err := b.checkAvailable("list"); err != nil {
		return nil, err
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.traceAction("output", "list")
	items, err := b.backend.List(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if openErr := b.session.ensureOpen(); openErr != nil {
		return nil, openErr
	}
	return items, b.operationError("list", err)
}

func (b *OutputBundle) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}
