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
	if b.Clipboard == nil {
		return "", &CapabilityError{Cap: CapabilityClipboard, Err: ErrNotAvailable}
	}
	b.traceAction("clipboard", "get")
	return b.Clipboard.Get(ctx)
}

func (b *ClipboardBundle) Set(ctx context.Context, text string) error {
	if b.Clipboard == nil {
		return &CapabilityError{Cap: CapabilityClipboard, Err: ErrNotAvailable}
	}
	b.traceAction("clipboard", "set")
	return b.Clipboard.Set(ctx, text)
}

func (b *ClipboardBundle) close() error {
	if b.Clipboard == nil {
		return nil
	}
	return b.Clipboard.Close()
}
