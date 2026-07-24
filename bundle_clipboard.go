package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/clipboard"
)

type ClipboardBundle struct {
	clipboard.Clipboard
	bundleBase
}

func (b *ClipboardBundle) Get(ctx context.Context) (string, error) {
	if b == nil || b.Clipboard == nil {
		return "", b.unavailable("get")
	}
	b.traceAction("clipboard", "get")
	return b.Clipboard.Get(ctx)
}

func (b *ClipboardBundle) Set(ctx context.Context, text string) error {
	if b == nil || b.Clipboard == nil {
		return b.unavailable("set")
	}
	b.traceAction("clipboard", "set")
	return b.Clipboard.Set(ctx, text)
}

func (b *ClipboardBundle) close() error {
	if b == nil || b.Clipboard == nil {
		return nil
	}
	return b.Clipboard.Close()
}

func (b *ClipboardBundle) pasteWithInputContext(
	ctx context.Context,
	text string,
	input *InputBundle,
) error {
	if err := b.Set(ctx, text); err != nil {
		return err
	}
	return input.typeContext(ctx, "{ctrl+v}")
}
