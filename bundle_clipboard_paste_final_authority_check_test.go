package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The clipboard may finish a Set before the caller revokes permission to
// dispatch keyboard input. A final owner check must prevent the shortcut
// even if both clipboard Set checks observed a live caller.
type monitoringPasteInput struct {
	*closeCountingInput
	typeCalls int
}

func (i *monitoringPasteInput) Type(context.Context, string) error {
	i.typeCalls++
	return nil
}

func TestClipboardPasteRejectsRevocationBetweenSetAndKeyboardDispatch(t *testing.T) {
	input := &monitoringPasteInput{closeCountingInput: &closeCountingInput{}}
	clipboard := &authorityLossClipboardBackend{}
	session := NewSessionForTesting(nil, input, nil, nil, clipboard)
	t.Cleanup(func() { _ = session.Close() })
	base, cancelDeadline := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelDeadline()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 3}
	err := session.Clipboard.pasteWithInputContext(ctx, "sensitive updated selection", session.Input)
	if !errors.Is(err, context.Canceled) || ctx.calls != 3 {
		t.Fatalf("post-clipboard cancellation: err=%v checks=%d", err, ctx.calls)
	}
	if clipboard.calls != 1 {
		t.Fatalf("clipboard backend was not called once: %d", clipboard.calls)
	}
	if input.typeCalls != 0 {
		t.Fatalf("unauthorized paste shortcut dispatched: %d", input.typeCalls)
	}
}
