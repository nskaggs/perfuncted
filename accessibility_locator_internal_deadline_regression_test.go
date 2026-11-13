package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorLateDeadlineSnapshot struct {
	*snapshotAccessibilityAutomationFake
	expired bool
}

func (b *locatorLateDeadlineSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	<-ctx.Done()
	b.expired = errors.Is(ctx.Err(), context.DeadlineExceeded)
	return b.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
}

// A backend may return a structurally complete tree after its bounded read
// deadline. The locator must refuse that stale tree even if the caller's outer
// context has not yet expired.
func TestAccessibilityLocatorRejectsSnapshotAfterInternalReadDeadline(t *testing.T) {
	for _, operation := range []string{"matches", "resolve"} {
		t.Run(operation, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			locator.bundle.session.config.Timeouts = TimeoutPolicy{Long: 10 * time.Millisecond}
			backend := &locatorLateDeadlineSnapshot{snapshotAccessibilityAutomationFake: fake}
			previous := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = previous })
			var found string
			var err error
			if operation == "matches" {
				var nodes []accessibility.Node
				nodes, err = locator.Matches(context.Background())
				if len(nodes) > 0 {
					found = nodes[0].Name
				}
			} else {
				var node accessibility.Node
				node, err = locator.Resolve(context.Background())
				found = node.Name
			}
			if !errors.Is(err, context.DeadlineExceeded) || found != "" || !backend.expired || fake.snapshotCalls != 1 {
				t.Fatalf("%s after provider read deadline: target=%q err=%v deadline=%t snapshots=%d", operation, found, err, backend.expired, fake.snapshotCalls)
			}
		})
	}
}
