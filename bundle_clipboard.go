package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/internal/util"
)

// ClipboardBundle wraps the clipboard interface.
type ClipboardBundle struct {
	clipboard.Clipboard
	tracer *actionTracer
}

// close delegates to the underlying Clipboard Close method.
func (c ClipboardBundle) close() error {
	if c.Clipboard == nil {
		return nil
	}
	c.traceAction("close")
	return c.Clipboard.Close()
}

func (c ClipboardBundle) checkAvailable() error {
	return util.CheckAvailable("clipboard", c.Clipboard)
}

func (c ClipboardBundle) traceAction(msg string) {
	if c.tracer == nil {
		return
	}
	c.tracer.Tracef("clipboard", "%s", msg)
}

func (c ClipboardBundle) Get() (string, error) {
	return c.getContext(context.Background())
}

func (c ClipboardBundle) getContext(ctx context.Context) (string, error) {
	c.traceAction("get")
	if err := c.checkAvailable(); err != nil {
		return "", err
	}
	return c.Clipboard.Get(ctx)
}

func (c ClipboardBundle) Set(text string) error {
	return c.setContext(context.Background(), text)
}

func (c ClipboardBundle) setContext(ctx context.Context, text string) error {
	c.traceAction(fmt.Sprintf("set text=%q", text))
	if err := c.checkAvailable(); err != nil {
		return err
	}
	return c.Clipboard.Set(ctx, text)
}

func (c ClipboardBundle) pasteWithInputContext(ctx context.Context, text string, inp InputBundle) error {
	c.traceAction(fmt.Sprintf("paste-with-input text=%q", text))
	if err := c.setContext(ctx, text); err != nil {
		return err
	}
	return inp.typeContext(ctx, "{ctrl+v}")
}
