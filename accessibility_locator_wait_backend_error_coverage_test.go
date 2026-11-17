package perfuncted

import (
	"context"
	"errors"
	"testing"
)

func TestAccessibilityLocatorWaitStopsOnPermanentBackendFailure(t *testing.T) {
	locator, backend := newLocatorWaitFixture(t, 1, false)
	failure := ErrApplicationExited
	backend.snapshotErr = failure

	node, evidence, err := locator.Wait(context.Background())
	if !errors.Is(err, failure) {
		t.Fatalf("locator wait error = %v, want permanent failure %v", err, failure)
	}
	if node.ID.BusName != "" || evidence.Evaluations != 1 || backend.snapshotCalls != 1 {
		t.Fatalf("permanent snapshot failure was retried or published as a match: node=%+v evidence=%+v snapshots=%d", node, evidence, backend.snapshotCalls)
	}
}
