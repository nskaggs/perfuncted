package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type revokingLocatorFillBackend struct {
	*snapshotAccessibilityAutomationFake
	after  func()
	writes int
}

func (b *revokingLocatorFillBackend) SetTextContents(_ context.Context, _ accessibility.NodeID, _ string) error {
	b.writes++
	if b.after != nil {
		b.after()
	}
	return nil
}

// A provider may ignore cancellation or session closure while performing a
// text mutation. The failed high-level operation must not disclose the
// previously resolved private node in its dispatch receipt.
func TestAccessibilityLocatorFillSuppressesReceiptAfterRevokedAuthority(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			session := locator.bundle.session
			backend := &revokingLocatorFillBackend{snapshotAccessibilityAutomationFake: fake}
			previous := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = previous })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := error(context.Canceled)
			if mode == "caller-cancel" {
				backend.after = cancel
			} else {
				want = ErrSessionClosed
				backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			}
			checks := 0
			receipt, _, err := locator.FillAndWait(ctx, "private-value", Predicate("verified", func(context.Context) (bool, error) { checks++; return true, nil }))
			if !errors.Is(err, want) || receipt.Node.ID.BusName != "" || receipt.Node.Name != "" || backend.writes != 1 || checks != 0 {
				t.Fatalf("%s leaked fill after revocation: receipt=%+v err=%v writes=%d checks=%d", mode, receipt, err, backend.writes, checks)
			}
		})
	}
}
