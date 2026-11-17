package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityLocatorActionRejectsAmbiguousTargetWithoutDispatch(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 2, false)
	checks := 0

	receipt, evidence, err := locator.ClickAndWait(context.Background(), Predicate("activated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ClickAndWait error = %v, want ambiguous target", err)
	}
	if receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 || checks != 0 {
		t.Fatalf("ambiguous action exposed a target or evaluated its postcondition: receipt=%+v evidence=%+v checks=%d", receipt, evidence, checks)
	}
	if fake.invokeCount != 0 || len(fake.calls) != 0 {
		t.Fatalf("ambiguous action dispatched provider calls %v, want none", fake.calls)
	}
	_, observationDone, _ := fake.eventObservationState()
	select {
	case <-observationDone:
	default:
		t.Fatal("ambiguous action left semantic event observation active")
	}
}

type locatorFailingScopeRefresh struct {
	*snapshotAccessibilityAutomationFake
	refreshCalls int
	refreshErr   error
}

func (b *locatorFailingScopeRefresh) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.refreshCalls++
	if b.refreshCalls == 2 {
		return accessibility.Application{}, b.refreshErr
	}
	return b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
}

func TestAccessibilityLocatorDoesNotRetryStaleActionWhenScopeRefreshFails(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	wantErr := errors.New("synthetic application identity lookup failure")
	fake.textErr = accessibility.ErrStaleGeneration
	backend := &locatorFailingScopeRefresh{snapshotAccessibilityAutomationFake: fake, refreshErr: wantErr}
	locator.bundle.backend = backend
	checks := 0

	receipt, evidence, err := locator.FillAndWait(context.Background(), "replacement", Predicate("updated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("FillAndWait error = %v, want scope refresh failure", err)
	}
	if backend.refreshCalls != 2 || fake.textCount != 1 {
		t.Fatalf("scope refreshes=%d text writes=%d, want initial resolution, one failed refresh, and no retry", backend.refreshCalls, fake.textCount)
	}
	if receipt.Node.ID.BusName == "" || receipt.Dispatch != accessibility.DispatchNotSent || evidence.Evaluations != 0 || checks != 0 {
		t.Fatalf("failed stale-action recovery returned unexpected evidence: receipt=%+v evidence=%+v checks=%d", receipt, evidence, checks)
	}
}
