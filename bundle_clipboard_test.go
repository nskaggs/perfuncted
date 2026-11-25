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

// The provider may be readable while explicitly lacking permission to write.
// Capability admission must reject Set without dispatching a mutation.
type readOnlyClipboardFixture struct{ *authorityLossClipboardBackend }

func (*readOnlyClipboardFixture) SupportedOperations() []string { return []string{"get"} }

func TestClipboardSetRejectsReadOnlyBackendBeforeMutation(t *testing.T) {
	backend := &readOnlyClipboardFixture{authorityLossClipboardBackend: &authorityLossClipboardBackend{}}
	session := NewSessionForTesting(nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Clipboard.Get(context.Background()); err != nil {
		t.Fatalf("readable clipboard not available: %v", err)
	}
	err := session.Clipboard.Set(context.Background(), "private content")
	if !errors.Is(err, ErrUnsupported) || backend.calls != 0 {
		t.Fatalf("read-only clipboard accepted mutation: err=%v backend calls=%d", err, backend.calls)
	}
}

type authorityLossClipboardBackend struct {
	after func()
	calls int
}

func (b *authorityLossClipboardBackend) Get(context.Context) (string, error) { return "", nil }
func (b *authorityLossClipboardBackend) Set(context.Context, string) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *authorityLossClipboardBackend) Close() error { return nil }

// The public facade must not dispatch an already-canceled mutation or report
// success after a clipboard provider loses the caller's/session's authority.
func TestClipboardSetRejectsAuthorityLoss(t *testing.T) {
	for _, mode := range []string{"pre-canceled", "late-canceled", "session-closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &authorityLossClipboardBackend{}
			session := NewSessionForTesting(nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			expected := context.Canceled
			if mode == "pre-canceled" {
				cancel()
			} else if mode == "late-canceled" {
				backend.after = cancel
			} else {
				expected = ErrSessionClosed
				backend.after = func() { _ = session.Close() }
			}
			err := session.Clipboard.Set(ctx, "private text")
			calls := 1
			if mode == "pre-canceled" {
				calls = 0
			}
			if !errors.Is(err, expected) || backend.calls != calls {
				t.Fatalf("mode %s returned %v; backend calls %d, want %d", mode, err, backend.calls, calls)
			}
		})
	}
}
