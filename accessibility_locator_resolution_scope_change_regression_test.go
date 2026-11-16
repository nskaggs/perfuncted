package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityLocatorRejectsScopeReplacementAfterStaleSnapshot(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	fake.snapshotErr = accessibility.ErrStaleGeneration
	first := fake.apps[0]
	replacement := first
	replacement.ID = accessibility.NodeID{
		BusName: "org.synthetic.Replacement", ObjectPath: "/replacement", Generation: 12,
	}
	fake.freshApps = []accessibility.Application{first, replacement}

	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, accessibility.ErrScope) || len(matches) != 0 {
		t.Fatalf("scope replacement after stale snapshot returned matches=%+v error=%v, want no matches and ErrScope", matches, err)
	}
	if fake.snapshotCalls != 1 || fake.freshApplicationFind != 2 {
		t.Fatalf("scope resolutions=%d snapshots=%d, want two resolutions and only the original snapshot", fake.freshApplicationFind, fake.snapshotCalls)
	}
}
