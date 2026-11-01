package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// An already-running dispatcher must never acknowledge a newly canceled
// event subscription. In particular, the optimization that reuses the active
// event transport must still respect the new caller's context.
func TestATSPIEventDispatcherReuseRejectsCanceledCaller(t *testing.T) {
	b, _ := newPrivateCacheBackend(t, nil)
	b.eventCancel = func() {}
	b.eventRegistered = append([]string(nil), semanticObservationEventFamilies[:]...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, requireSemantic := range []bool{false, true} {
		if err := b.startEvents(ctx, requireSemantic); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller reused event dispatcher (semantic=%t): %v", requireSemantic, err)
		}
	}
	if err := b.startEvents(context.Background(), false); err != nil {
		t.Fatalf("healthy event subscriber denied existing dispatcher: %v", err)
	}
	if err := b.startEvents(context.Background(), true); err != nil {
		t.Fatalf("semantic subscriber denied complete dispatcher: %v", err)
	}
	// An existing notification-only dispatcher cannot claim semantic event
	// coverage that the provider never registered.
	b.eventRegistered = []string{"object:property-change"}
	if err := b.startEvents(context.Background(), true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("incomplete event dispatcher claimed semantic coverage: %v", err)
	}
	if err := b.startEvents(context.Background(), false); err != nil {
		t.Fatalf("notification-only subscription rejected: %v", err)
	}
}

// This wrapper exposes exactly the first context read so a real cancellation
// can race with a blocked backend connection check, without sleeps or polls.
type observedEventContext struct {
	context.Context
	readOnce  sync.Once
	firstRead chan struct{}
}

func (ctx *observedEventContext) Err() error {
	ctx.readOnce.Do(func() { close(ctx.firstRead) })
	return ctx.Context.Err()
}

func TestATSPIEventDispatcherReuseNotAcknowledgedWhenCallerCancelsDuringAdmission(t *testing.T) {
	b, _ := newPrivateCacheBackend(t, nil)
	b.eventCancel = func() {}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedEventContext{Context: base, firstRead: make(chan struct{})}
	b.mu.Lock() // connected() must observe the transport while the lock is held.
	result := make(chan error, 1)
	go func() { result <- b.startEvents(ctx, false) }()
	select {
	case <-ctx.firstRead:
	case <-time.After(time.Second):
		b.mu.Unlock()
		t.Fatal("event admission did not read caller context")
	}
	cancel()
	b.mu.Unlock()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller acquired an already-running event stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("event admission failed to terminate after cancellation")
	}
}
