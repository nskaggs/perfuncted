package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorRetryChangedApplicationBackend struct {
	*snapshotAccessibilityAutomationFake
	refreshes int
}

func (b *locatorRetryChangedApplicationBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.refreshes++
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	if b.refreshes == 2 {
		app.ID.BusName = "org.private.ReplacedApplication"
		app.ID.ObjectPath = "/replacement"
	}
	return app, err
}

// A rejected, not-sent mutation does not grant the caller lasting access to
// the original target once scope ownership changes during retry validation.
func TestAccessibilityLocatorRetrySuppressesOldReceiptOnScopeReplacement(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	fake.textErr = accessibility.ErrStaleGeneration
	backend := &locatorRetryChangedApplicationBackend{snapshotAccessibilityAutomationFake: fake}
	locator.bundle.backend = backend
	receipt, _, err := locator.FillAndWait(context.Background(), "private-value", Predicate("verified", func(context.Context) (bool, error) { return true, nil }))
	if !errors.Is(err, ErrManagedScopeChanged) || receipt.Node.ID.BusName != "" || receipt.Node.Name != "" || backend.refreshes != 2 || fake.textCount != 1 {
		t.Fatalf("scope replacement exposed stale private target: receipt=%+v err=%v refreshes=%d writes=%d", receipt, err, backend.refreshes, fake.textCount)
	}
}
