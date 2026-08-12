package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle is the session-bound window discovery and control facade.
type WindowBundle struct {
	backend window.Manager
	bundleBase
}

func (b *WindowBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.bundleBase.checkAvailable(operation, b.backend != nil)
}

func (b *WindowBundle) close() error {
	if b == nil || b.backend == nil {
		return nil
	}
	return b.backend.Close()
}

// Sync refreshes pending window-backend state when supported.
func (b *WindowBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		return b.operationError("sync", backend.Sync(ctx))
	}
	return b.operationError("sync", ErrUnsupported)
}

// ActiveTitle returns the title of the focused window.
func (b *WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	if err := b.checkAvailable("active-title"); err != nil {
		return "", err
	}
	title, err := b.backend.ActiveTitle(ctx)
	return title, b.operationError("active-title", err)
}
