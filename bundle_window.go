package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle is the session-bound window discovery and control facade.
type WindowBundle struct {
	backend window.Manager
	bundleBase
	session *Session
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

func (b *WindowBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		return backend.Sync(ctx)
	}
	return nil
}

func (b *WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	if err := b.checkAvailable("active-title"); err != nil {
		return "", err
	}
	return b.backend.ActiveTitle(ctx)
}
