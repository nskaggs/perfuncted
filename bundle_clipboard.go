package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/clipboard"
)

// ClipboardBundle exposes clipboard operations through a Session.
type ClipboardBundle struct {
	backend clipboard.Clipboard
	bundleBase
}

func (b *ClipboardBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.checkBackend(operation, b.backend)
}

// Get returns the current clipboard text.
func (b *ClipboardBundle) Get(ctx context.Context) (string, error) {
	if b == nil {
		return "", (&bundleBase{}).unavailable("get")
	}
	if err := b.checkAvailable("get"); err != nil {
		return "", err
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	b.traceAction("clipboard", "get")
	text, err := b.backend.Get(ctx)
	return text, b.operationError("get", err)
}

// Set replaces the clipboard text.
func (b *ClipboardBundle) Set(ctx context.Context, text string) error {
	if b == nil {
		return (&bundleBase{}).unavailable("set")
	}
	if err := b.checkAvailable("set"); err != nil {
		return err
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	b.traceAction("clipboard", "set")
	return b.operationError("set", b.backend.Set(ctx, text))
}

func (b *ClipboardBundle) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}

func (b *ClipboardBundle) pasteWithInputContext(
	ctx context.Context,
	text string,
	input *InputBundle,
) error {
	if input == nil {
		if b == nil {
			return (&bundleBase{}).unavailable("paste")
		}
		return b.unavailable("paste")
	}
	if err := input.checkAvailable("keyboard"); err != nil {
		return err
	}
	if err := b.Set(ctx, text); err != nil {
		return err
	}
	return input.typeContext(ctx, "{ctrl+v}")
}
