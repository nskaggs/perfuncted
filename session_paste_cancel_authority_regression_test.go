package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type cancelDuringClipboardSet struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancelDuringClipboardSet) Get(context.Context) (string, error) { return "", nil }
func (c *cancelDuringClipboardSet) Set(context.Context, string) error {
	c.calls++
	c.cancel()
	return nil
}
func (c *cancelDuringClipboardSet) Close() error { return nil }

// Permission to send desktop input is withdrawn at cancellation. A backend
// ignoring its context must not be able to dispatch a canceled paste.
func TestPasteRejectsCanceledRequestBeforeDesktopSideEffects(t *testing.T) {
	for _, withClipboard := range []bool{false, true} {
		name := "literal-fallback"
		if withClipboard {
			name = "clipboard-path"
		}
		t.Run(name, func(t *testing.T) {
			input := &pftest.Inputter{}
			var clipboard *pftest.Clipboard
			if withClipboard {
				clipboard = &pftest.Clipboard{Text: "existing"}
			}
			session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
			t.Cleanup(func() { _ = session.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := session.Paste(ctx, "sensitive")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled paste: %v", err)
			}
			if len(input.Calls) != 0 {
				t.Fatalf("canceled paste sent keystrokes: %v", input.Calls)
			}
			if withClipboard && clipboard.Text != "existing" {
				t.Fatalf("canceled paste mutated clipboard: %q", clipboard.Text)
			}
		})
	}
}

// Cancellation while a clipboard backend is updating content must not
// authorize a follow-up keyboard shortcut, even if Set returns nil.
func TestPasteDoesNotTypeAfterClipboardSetCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clipboard := &cancelDuringClipboardSet{cancel: cancel}
	input := &pftest.Inputter{}
	session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Paste(ctx, "new selection")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled clipboard operation reported %v", err)
	}
	if clipboard.calls != 1 {
		t.Fatalf("clipboard Set called %d times", clipboard.calls)
	}
	if len(input.Calls) != 0 {
		t.Fatalf("keyboard shortcut sent after cancellation: %v", input.Calls)
	}
}

type cancelAfterInputAcknowledgement struct {
	pftest.Inputter
	cancel     context.CancelFunc
	dispatched int
}

func (i *cancelAfterInputAcknowledgement) Type(ctx context.Context, text string) error {
	i.dispatched++
	i.cancel()
	return nil
}
func (i *cancelAfterInputAcknowledgement) TypeLiteral(ctx context.Context, text string) error {
	i.dispatched++
	i.cancel()
	return nil
}

// A provider may acknowledge dispatch after the request is revoked; do not
// represent that observation as a verified successful paste.
func TestPasteRejectsSuccessAcknowledgedAfterCancellation(t *testing.T) {
	for _, withClipboard := range []bool{false, true} {
		name := "literal-fallback"
		if withClipboard {
			name = "clipboard-shortcut"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			input := &cancelAfterInputAcknowledgement{cancel: cancel}
			var clipboard *pftest.Clipboard
			if withClipboard {
				clipboard = &pftest.Clipboard{}
			}
			session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
			t.Cleanup(func() { _ = session.Close() })
			err := session.Paste(ctx, "payload")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late-canceled paste returned %v, want cancellation", err)
			}
			if input.dispatched != 1 {
				t.Fatalf("expected one attempted input operation, got %d", input.dispatched)
			}
		})
	}
}
