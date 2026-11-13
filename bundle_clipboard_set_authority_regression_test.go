package perfuncted

import (
	"context"
	"errors"
	"testing"
)

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
			expected := error(context.Canceled)
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
