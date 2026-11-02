package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// The event and snapshot stand-ins deliberately ignore canceled contexts.
// Pre-canceled semantic reads must never query or reveal provider-owned data.
func TestAccessibilityLocatorDoesNotReadOrDiscloseSnapshotAfterPreCancellation(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled locator disclosed matches=%+v error=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled request dispatched %d provider snapshot reads", fake.snapshotCalls)
	}
	node, err := loc.Resolve(ctx)
	if !errors.Is(err, context.Canceled) || node.ID.BusName != "" {
		t.Fatalf("canceled locator disclosed target=%+v error=%v", node, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled resolve dispatched %d provider snapshot reads", fake.snapshotCalls)
	}
}

// A remote AT-SPI provider may answer after the caller canceled while the
// snapshot was in flight. The response must not be exposed even if complete.
type locatorCancelingSnapshot struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	result, err := f.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	f.cancel()
	return result, err
}

func TestAccessibilityLocatorRejectsLateCancellationDuringProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingSnapshot{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled provider reply disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 1 {
		t.Fatalf("fixture must cancel after a real snapshot, got %d calls", fake.snapshotCalls)
	}
}

type locatorCancelingSubscription struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingSubscription) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	events, err := f.snapshotAccessibilityAutomationFake.Events(ctx, opts)
	f.cancel()
	return events, err
}

func TestAccessibilityLocatorCancellationDuringEventSubscriptionSkipsProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingSubscription{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled event subscription disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled event setup issued %d forbidden snapshot reads", fake.snapshotCalls)
	}
}

type locatorCancelingApplication struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingApplication) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	app, err := f.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	f.cancel()
	return app, err
}

func TestAccessibilityLocatorCancellationDuringScopeResolutionSkipsProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingApplication{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled scope lookup disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled scope lookup issued %d forbidden snapshot reads", fake.snapshotCalls)
	}
}
