package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/internal/util"
)

// ClipboardBundle wraps the clipboard interface.
type ClipboardBundle struct {
	clipboard.Clipboard
}

func (c ClipboardBundle) checkAvailable() error {
	return util.CheckAvailable("clipboard", c.Clipboard)
}

func (c ClipboardBundle) Get() (string, error) {
	return c.GetContext(context.Background())
}

func (c ClipboardBundle) GetContext(ctx context.Context) (string, error) {
	if err := c.checkAvailable(); err != nil {
		return "", err
	}
	return c.Clipboard.Get(ctx)
}

func (c ClipboardBundle) Set(text string) error {
	return c.SetContext(context.Background(), text)
}

func (c ClipboardBundle) SetContext(ctx context.Context, text string) error {
	if err := c.checkAvailable(); err != nil {
		return err
	}
	return c.Clipboard.Set(ctx, text)
}

func (c ClipboardBundle) PasteWithInput(text string, inp InputBundle) error {
	return c.PasteWithInputContext(context.Background(), text, inp)
}

const PasteCombo = "ctrl+v"

func (c ClipboardBundle) PasteWithInputContext(ctx context.Context, text string, inp InputBundle) error {
	if err := c.SetContext(ctx, text); err != nil {
		return err
	}
	return inp.PressComboContext(ctx, PasteCombo)
}
