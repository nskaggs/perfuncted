package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityOwnerNilAndRetiredLeaseBoundaries(t *testing.T) {
	var nilOwner *accessibilityBackendOwner
	if err := nilOwner.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	nilOwner.Invalidate(accessibility.NodeID{})
	if nilOwner.isCurrent(nil) {
		t.Fatal("nil owner considered current")
	}
	if _, err := nilOwner.acquireGeneration(nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner generation lease: %v", err)
	}
	if _, err := newAccessibilityBackendOwner(nil).acquireGeneration(nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil generation acquired: %v", err)
	}
	var nilLease *accessibilityBackendLease
	nilLease.release()
	var nilBinding *accessibilityEventBinding
	nilBinding.complete(accessibility.ErrDisconnected)
}

func TestAccessibilityOwnerRetiredLeaseClosesAfterLastOwnerReleases(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	if owner.retireLocked(nil) {
		t.Fatal("nil generation was retired")
	}
	backend := newOwnerLifecycleBackend(51, "applications")
	owner.install(backend)
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.hasBackend() {
		t.Fatal("installed backend unavailable")
	}
	owner.mu.Lock()
	immediate := owner.retireLocked(lease.gen)
	repeated := owner.retireLocked(lease.gen)
	owner.mu.Unlock()
	if immediate || repeated {
		t.Fatalf("active or already retired generation closed prematurely: first=%v repeat=%v", immediate, repeated)
	}
	if owner.hasBackend() {
		t.Fatal("retired backend still advertised")
	}
	lease.release()
	select {
	case <-backend.closeDone:
	case <-time.After(time.Second):
		t.Fatal("retired generation not released when final lease exited")
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("retired backend closed %d times", backend.closeCalls.Load())
	}
	lease.release()
	if backend.closeCalls.Load() != 1 {
		t.Fatal("lease release was not idempotent")
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("closing retired owner: %v", err)
	}
}

func TestAccessibilityOwnerCanceledAndNilOperationContextNeverCallsBackend(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	backend := newOwnerLifecycleBackend(11, "applications")
	owner.install(backend)
	defer owner.Close()
	calls := 0
	callback := func(accessibility.Backend, context.Context) error { calls++; return nil }
	var nilCtx context.Context
	if err := owner.withBackendContext(nilCtx, callback); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil context accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.withBackendContext(ctx, callback); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call succeeded: %v", err)
	}
	if calls != 0 {
		t.Fatalf("pre-canceled backend call executed %d times", calls)
	}
	if err := owner.withBackendContext(context.Background(), callback); err != nil || calls != 1 {
		t.Fatalf("authorized call result=%v calls=%d", err, calls)
	}
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	derived, done := lease.context(nilCtx)
	if derived.Err() != nil {
		t.Fatalf("nil parent was not normalized: %v", derived.Err())
	}
	done()
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatalf("context cleanup did not revoke lease: %v", derived.Err())
	}
	lease.release()
	if _, err := owner.eventsAcrossReopens(ctx, accessibility.EventOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled persistent event request was accepted: %v", err)
	}
	if err := owner.reopen(nilCtx); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil reopen context accepted: %v", err)
	}
}

func TestAccessibilityEventBindingCancellationRetirementAndIdempotentCompletion(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(nil)
	owner.current = gen
	owner.generations[gen] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 2), done: make(chan struct{})}
	if got := sub.beginBinding(gen); got == nil {
		t.Fatal("binding was not created")
	}
	first := sub.beginBinding(gen)
	if reused := sub.beginBinding(gen); reused != first {
		t.Fatal("duplicate binding replaced current identity")
	}
	if err := sub.waitBinding(context.Background(), nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil binding accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sub.waitBinding(ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding wait: %v", err)
	}
	first.complete(errors.New("synthetic provider denied"))
	first.complete(nil)
	if err := sub.waitBinding(context.Background(), first); err == nil || !strings.Contains(err.Error(), "provider denied") {
		t.Fatalf("binding completion overwritten: %v", err)
	}
	sub.finishStream()
	sub.finishStream()
	if fresh := sub.beginBinding(newAccessibilityBackendGeneration(nil)); fresh != nil {
		t.Fatal("finished event stream opened new binding")
	}
	if err := sub.waitBinding(context.Background(), &accessibilityEventBinding{done: make(chan struct{})}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("finished subscription blocked or accepted: %v", err)
	}
	closeOwner := newAccessibilityBackendOwner(nil)
	if err := closeOwner.Close(); err != nil {
		t.Fatal(err)
	}
	if closeOwner.isCurrent(gen) {
		t.Fatal("closed owner admitted foreign generation")
	}
}

func TestAccessibilityReopenBindingFailsOnCanceledCallerAndRetiredGeneration(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(73, "events"))
	owner.current = gen
	owner.generations[gen] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sub.nextBinding(ctx, gen); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reopen wait: %v", err)
	}
	owner.mu.Lock()
	closeNow := owner.retireLocked(gen)
	owner.mu.Unlock()
	if closeNow {
		owner.closeGeneration(gen)
	}
	if _, err := sub.nextBinding(context.Background(), gen); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("retired unchanged generation rebound: %v", err)
	}
	owner.mu.Lock()
	owner.current = newAccessibilityBackendGeneration(newOwnerLifecycleBackend(74, "events"))
	owner.generations[owner.current] = struct{}{}
	owner.mu.Unlock()
	binding, err := sub.nextBinding(context.Background(), gen)
	if err != nil || binding == nil || binding.gen != owner.current {
		t.Fatalf("fresh generation binding=%v err=%v", binding, err)
	}
	sub.finishStream()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
