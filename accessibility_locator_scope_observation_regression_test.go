package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorScopeInvalidationBackend struct {
	*snapshotAccessibilityAutomationFake
	stream chan accessibility.Event
}

func (b *locatorScopeInvalidationBackend) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.stream = make(chan accessibility.Event, 1)
	return b.stream, nil
}
func (b *locatorScopeInvalidationBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	b.stream <- accessibility.Event{Kind: "object:children-changed"}
	return app, err
}

// A semantic change during application-scope resolution invalidates the
// observation before snapshot dispatch; there is no reason to query that
// already-inconsistent scope or expose its target.
func TestAccessibilityLocatorRejectsScopeInvalidationBeforeSnapshot(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	backend := &locatorScopeInvalidationBackend{snapshotAccessibilityAutomationFake: fake}
	previous := locator.bundle.backend
	locator.bundle.backend = backend
	t.Cleanup(func() { locator.bundle.backend = previous })
	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, accessibility.ErrObservationChanged) || len(matches) != 0 || fake.snapshotCalls != 0 {
		t.Fatalf("scope invalidation: matches=%+v err=%v snapshots=%d", matches, err, fake.snapshotCalls)
	}
}
