package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorRetryRevokingScope struct {
	*snapshotAccessibilityAutomationFake
	onRefresh  func()
	scopeCalls int
}

func (b *locatorRetryRevokingScope) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.scopeCalls++
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	if b.scopeCalls == 2 && b.onRefresh != nil {
		b.onRefresh()
	}
	return app, err
}

// A stale, not-sent action may leave a receipt from the first resolution.
// Revocation during the required scope refresh cannot publish that receipt.
func TestAccessibilityLocatorRetryDoesNotDiscloseReceiptAfterScopeRevocation(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			fake.textErr = accessibility.ErrStaleGeneration
			backend := &locatorRetryRevokingScope{snapshotAccessibilityAutomationFake: fake}
			locator.bundle.backend = backend
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := error(context.Canceled)
			if mode == "caller-cancel" {
				backend.onRefresh = cancel
			} else {
				want = ErrSessionClosed
				backend.onRefresh = func() {
					session := locator.bundle.session
					session.lifecycleMu.Lock()
					session.closed = true
					session.lifecycleMu.Unlock()
				}
			}
			receipt, _, err := locator.FillAndWait(ctx, "redacted", Predicate("done", func(context.Context) (bool, error) { return true, nil }))
			if !errors.Is(err, want) || receipt.Node.ID.BusName != "" || receipt.Action.Name != "" || backend.scopeCalls != 2 || fake.textCount != 1 {
				t.Fatalf("%s leaked first action receipt after revocation: receipt=%+v err=%v scopeCalls=%d writes=%d", mode, receipt, err, backend.scopeCalls, fake.textCount)
			}
		})
	}
}
