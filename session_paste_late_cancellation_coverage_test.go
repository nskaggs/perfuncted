package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

type cancelAfterSetClipboard struct {
	pftest.Clipboard
	cancel   context.CancelFunc
	setCalls int
}

func (c *cancelAfterSetClipboard) Set(_ context.Context, text string) error {
	c.setCalls++
	c.Text = text
	c.cancel()
	return nil
}

type cancelAfterLiteralInput struct {
	pftest.Inputter
	cancel context.CancelFunc
}

func (i *cancelAfterLiteralInput) TypeLiteral(ctx context.Context, text string) error {
	if err := i.Inputter.TypeLiteral(ctx, text); err != nil {
		return err
	}
	i.cancel()
	return nil
}

func TestPasteReturnsCancellationAfterLateBackendSuccess(t *testing.T) {
	t.Run("clipboard", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clipboard := &cancelAfterSetClipboard{cancel: cancel}
		input := &pftest.Inputter{}
		session := pftest.New(nil, input, nil, clipboard)
		defer session.Close()

		err := session.Paste(ctx, "private clipboard text")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Paste error = %v, want context.Canceled", err)
		}
		if clipboard.Text != "private clipboard text" || clipboard.setCalls != 1 {
			t.Fatalf("clipboard text=%q sets=%d, want one completed clipboard write", clipboard.Text, clipboard.setCalls)
		}
		if len(input.Calls) != 0 {
			t.Fatalf("paste shortcut was dispatched after cancellation: %v", input.Calls)
		}
	})

	t.Run("literal input fallback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		input := &cancelAfterLiteralInput{cancel: cancel}
		session := pftest.New(nil, input, nil, nil)
		defer session.Close()

		err := session.Paste(ctx, "late typed text")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Paste fallback error = %v, want context.Canceled", err)
		}
		if len(input.Calls) != 1 || input.Calls[0] != "type-literal:late typed text" {
			t.Fatalf("input dispatches = %v, want one literal-text dispatch", input.Calls)
		}
	})
}
