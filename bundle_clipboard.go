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

// Get returns the current clipboard text.
func (b *ClipboardBundle) Get(ctx context.Context) (string, error) {
	if b == nil {
		return "", (&bundleBase{}).unavailable("get")
	}
	if err := b.checkAvailable("get", b.backend != nil); err != nil {
		return "", err
	}
	b.traceAction("clipboard", "get")
	text, err := b.backend.Get(ctx)
	return text, b.operationError("get", err)
}

// Set replaces the clipboard text.
func (b *ClipboardBundle) Set(ctx context.Context, text string) error {
	if b == nil {
		return (&bundleBase{}).unavailable("set")
	}
	if err := b.checkAvailable("set", b.backend != nil); err != nil {
		return err
	}
	b.traceAction("clipboard", "set")
	return b.operationError("set", b.backend.Set(ctx, text))
}

func (b *ClipboardBundle) close() error {
	if b == nil || b.backend == nil {
		return nil
	}
	return b.backend.Close()
}

func (b *ClipboardBundle) pasteWithInputContext(
	ctx context.Context,
	text string,
	input *InputBundle,
) error {
	if input == nil {
		return ErrUnavailable
	}
	if err := input.checkAvailable("keyboard"); err != nil {
		return err
	}
	if err := b.Set(ctx, text); err != nil {
		return err
	}
	return input.typeContext(ctx, "{ctrl+v}")
}
