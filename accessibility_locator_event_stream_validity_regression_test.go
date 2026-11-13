package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorLostObservationBackend struct {
	*snapshotAccessibilityAutomationFake
	closedBeforeRead bool
	stream           chan accessibility.Event
}

func (b *locatorLostObservationBackend) Events(_ context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.stream = make(chan accessibility.Event, 1)
	if b.closedBeforeRead {
		close(b.stream)
	}
	return b.stream, nil
}
func (b *locatorLostObservationBackend) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	snapshot, err := b.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	if !b.closedBeforeRead {
		close(b.stream)
	}
	return snapshot, err
}

// A complete snapshot is not a stable semantic target unless its event
// subscription remained live throughout resolution.
func TestAccessibilityLocatorRejectsLostEventObservation(t *testing.T) {
	for _, phase := range []string{"before-snapshot", "during-snapshot"} {
		t.Run(phase, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			backend := &locatorLostObservationBackend{snapshotAccessibilityAutomationFake: fake, closedBeforeRead: phase == "before-snapshot"}
			old := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = old })
			matches, err := locator.Matches(context.Background())
			if !errors.Is(err, accessibility.ErrObservationChanged) || len(matches) != 0 {
				t.Fatalf("lost observation at %s exposed %d matches: %v", phase, len(matches), err)
			}
			if phase == "before-snapshot" && fake.snapshotCalls != 0 {
				t.Fatalf("snapshot dispatched after missing observation: %d", fake.snapshotCalls)
			}
		})
	}
}
