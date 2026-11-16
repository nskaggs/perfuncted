package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type locatorApplicationAppearsBackend struct {
	*snapshotAccessibilityAutomationFake
	lookups int
}

func (b *locatorApplicationAppearsBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.lookups++
	if b.lookups == 1 {
		return accessibility.Application{}, accessibility.ErrNotFound
	}
	return b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
}

func TestAccessibilityLocatorWaitFindsApplicationAfterInitialAbsence(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	backend := &locatorApplicationAppearsBackend{snapshotAccessibilityAutomationFake: fake}
	locator.bundle.backend = backend

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	node, evidence, err := locator.Wait(ctx, WaitEvery(time.Millisecond))
	if err != nil || node.Name != "Save" || node.ID.ObjectPath != "/save" {
		t.Fatalf("wait after scoped application appeared returned node=%+v error=%v", node, err)
	}
	if evidence.Evaluations != 2 || backend.lookups != 2 || fake.snapshotCalls != 1 {
		t.Fatalf("evaluations=%d application lookups=%d snapshots=%d, want two evaluations, two lookups, and one snapshot", evidence.Evaluations, backend.lookups, fake.snapshotCalls)
	}
}

func TestAccessibilityLocatorMatchesPropagatesApplicationProviderError(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	failure := errors.New("provider rejected scoped application lookup")
	backend := &locatorApplicationLookupFailureBackend{
		snapshotAccessibilityAutomationFake: fake,
		failure:                             failure,
	}
	locator.bundle.backend = backend

	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, failure) || len(matches) != 0 {
		t.Fatalf("provider lookup failure returned matches=%+v error=%v", matches, err)
	}
	if backend.lookups != 1 || fake.snapshotCalls != 0 {
		t.Fatalf("application lookups=%d snapshots=%d, want one lookup and no snapshot", backend.lookups, fake.snapshotCalls)
	}
}

type locatorApplicationLookupFailureBackend struct {
	*snapshotAccessibilityAutomationFake
	failure error
	lookups int
}

func (b *locatorApplicationLookupFailureBackend) FindApplicationFresh(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.lookups++
	return accessibility.Application{}, b.failure
}
