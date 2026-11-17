package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityOwnerAdvancesReopenedBackendThatReusesGeneration(t *testing.T) {
	old := newOwnerLifecycleBackend(44, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(44, "applications", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("reopen backend with reused generation: %v", err)
	}
	if got, want := fresh.Generation(), uint64(45); got != want {
		t.Fatalf("fresh backend generation = %d, want invalidated generation %d", got, want)
	}
	if got := owner.Generation(); got != fresh.Generation() || !owner.hasBackend() {
		t.Fatalf("owner published generation %d with backend available=%t; fresh generation=%d", got, owner.hasBackend(), fresh.Generation())
	}
	if got := old.closeCalls.Load(); got != 1 {
		t.Fatalf("retired backend close calls = %d, want one", got)
	}
	if got := fresh.closeCalls.Load(); got != 0 {
		t.Fatalf("published backend was closed %d times before session teardown", got)
	}
}

func TestAccessibilityOwnerRejectsFreshBackendAfterConcurrentOwnerClose(t *testing.T) {
	old := newOwnerLifecycleBackend(51, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(52, "applications", "reopen")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)
	owner.mu.Lock()
	oldGeneration := owner.current
	owner.mu.Unlock()
	closeDone := make(chan error, 1)
	old.reopen = func(context.Context) (accessibility.Backend, error) {
		go func() { closeDone <- owner.Close() }()
		awaitOwnerSignal(t, oldGeneration.retiredCh, "owner closure during backend reopen")
		return fresh, nil
	}

	err := session.Accessibility.Reopen(context.Background())
	if !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("reopen after owner closure = %v, want disconnected", err)
	}
	if closeErr := awaitOwnerSignal(t, closeDone, "owner close after reopen lease release"); closeErr != nil {
		t.Fatalf("owner.Close: %v", closeErr)
	}
	if owner.hasBackend() {
		t.Fatal("closed owner published a replacement backend")
	}
	if got := fresh.closeCalls.Load(); got != 1 {
		t.Fatalf("rejected replacement close calls = %d, want one", got)
	}
	if got := old.closeCalls.Load(); got != 1 {
		t.Fatalf("retired backend close calls = %d, want one", got)
	}
}
