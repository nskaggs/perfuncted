package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type authorityLossSyncBackend struct {
	pftest.Inputter
	after   func()
	calls   int
	failure error
}

func (b *authorityLossSyncBackend) Sync(context.Context) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return b.failure
}

// An acknowledged sync cannot certify pending desktop input after its caller
// canceled or the owner closed. A pre-canceled request must not dispatch.
func TestInputSyncRejectsPreflightAndLateAuthorityLoss(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &authorityLossSyncBackend{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			wantErr := error(context.Canceled)
			if mode == "pre-cancel" {
				cancel()
			} else if mode == "late-cancel" {
				backend.after = cancel
			} else {
				wantErr = perfuncted.ErrSessionClosed
				backend.after = func() { _ = session.Close() }
			}
			err := session.Input.Sync(ctx)
			wantCalls := 1
			if mode == "pre-cancel" {
				wantCalls = 0
			}
			if !errors.Is(err, wantErr) || backend.calls != wantCalls {
				t.Fatalf("Sync after %s: err=%v calls=%d want err=%v calls=%d", mode, err, backend.calls, wantErr, wantCalls)
			}
		})
	}
}
