package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorClosingSnapshot struct {
	*snapshotAccessibilityAutomationFake
	after func()
}

func (f *locatorClosingSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	result, err := f.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	if f.after != nil {
		f.after()
	}
	return result, err
}

// A semantic locator cannot publish nodes from a snapshot which finished
// only after its owning session was closed during provider execution.
func TestAccessibilityLocatorRejectsSnapshotAfterSessionClosure(t *testing.T) {
	for _, operation := range []string{"matches", "resolve"} {
		t.Run(operation, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			session := locator.bundle.session
			backend := &locatorClosingSnapshot{snapshotAccessibilityAutomationFake: fake}
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var found string
			var err error
			if operation == "matches" {
				var nodes []accessibility.Node
				nodes, err = locator.Matches(context.Background())
				if len(nodes) != 0 {
					found = nodes[0].Name
				}
			} else {
				var node accessibility.Node
				node, err = locator.Resolve(context.Background())
				found = node.Name
			}
			if !errors.Is(err, ErrSessionClosed) || found != "" || fake.snapshotCalls != 1 {
				t.Fatalf("%s disclosed locator target after session closure: target=%q err=%v snapshots=%d", operation, found, err, fake.snapshotCalls)
			}
		})
	}
}
