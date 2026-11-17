package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type cancelOnSuccessfulEventRetirement struct {
	*ownerLifecycleBackend
	cancel context.CancelFunc
}

func (b *cancelOnSuccessfulEventRetirement) RetireEventsForReopen(context.Context) error {
	b.cancel()
	return nil
}

func TestAccessibilityReopenCancellationDuringRetirementClosesFreshBackend(t *testing.T) {
	old := newOwnerLifecycleBackend(40, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(41, "applications", "reopen")
	old.retireEntered = make(chan struct{})
	old.retireGate = make(chan struct{})
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reopened := make(chan error, 1)
	go func() { reopened <- session.Accessibility.Reopen(ctx) }()
	awaitOwnerSignal(t, old.retireEntered, "event retirement during reopen")
	cancel()
	if err := awaitOwnerSignal(t, reopened, "canceled reopen after event retirement"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen after caller cancellation returned %v, want context.Canceled", err)
	}
	if !owner.hasBackend() || owner.Generation() != 40 || old.closeCalls.Load() != 0 {
		t.Fatalf("canceled reopen replaced or retired the current backend: available=%t generation=%d old closes=%d", owner.hasBackend(), owner.Generation(), old.closeCalls.Load())
	}
	if fresh.closeCalls.Load() != 1 {
		t.Fatalf("canceled reopen closed fresh backend %d times, want once", fresh.closeCalls.Load())
	}
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("current backend stopped serving applications after canceled reopen: %v", err)
	}
}

func TestAccessibilityReopenChecksCancellationAfterSuccessfulRetirement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &cancelOnSuccessfulEventRetirement{
		ownerLifecycleBackend: newOwnerLifecycleBackend(42, "applications", "reopen"),
		cancel:                cancel,
	}
	fresh := newOwnerLifecycleBackend(43, "applications", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	if err := session.Accessibility.Reopen(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen after successful retirement canceled its caller with %v, want context.Canceled", err)
	}
	if !owner.hasBackend() || owner.Generation() != 42 || old.closeCalls.Load() != 0 {
		t.Fatalf("canceled reopen replaced or retired the current backend: available=%t generation=%d old closes=%d", owner.hasBackend(), owner.Generation(), old.closeCalls.Load())
	}
	if fresh.closeCalls.Load() != 1 {
		t.Fatalf("canceled reopen closed fresh backend %d times, want once", fresh.closeCalls.Load())
	}
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("current backend stopped serving applications after canceled reopen: %v", err)
	}
}
