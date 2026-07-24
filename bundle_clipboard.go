package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/clipboard"
)

type ClipboardBundle struct {
	clipboard.Clipboard
	bundleBase
}

func (c ClipboardBundle) close() error {
	if c.Clipboard == nil {
		return nil
	}
	c.traceAction("clipboard", "close")
	return c.Clipboard.Close()
}

func (c ClipboardBundle) checkAvailable() error {
	return checkAvailable(c.Clipboard, "clipboard")
}

func (c ClipboardBundle) Get(ctx context.Context) (string, error) {
	return c.getContext(ctx)
}

func (c ClipboardBundle) getContext(ctx context.Context) (string, error) {
	c.traceAction("clipboard", "get")
	if err := c.checkAvailable(); err != nil {
		return "", err
	}
	return c.Clipboard.Get(ctx)
}

func (c ClipboardBundle) Set(ctx context.Context, text string) error {
	return c.setContext(ctx, text)
}

func (c ClipboardBundle) setContext(ctx context.Context, text string) error {
	c.traceAction("clipboard", "set text=%q", text)
	if err := c.checkAvailable(); err != nil {
		return err
	}
	return c.Clipboard.Set(ctx, text)
}

func (c ClipboardBundle) pasteWithInputContext(ctx context.Context, text string, inp InputBundle) error {
	c.traceAction("clipboard", "paste-with-input text=%q", text)
	if err := c.setContext(ctx, text); err != nil {
		return err
	}
	return inp.typeContext(ctx, "{ctrl+v}")
}
