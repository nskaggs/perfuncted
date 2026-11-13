package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type notifyEventWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *notifyEventWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// Subscriber admission must not race a departing dispatcher: a caller that
// cancels while waiting for the old run must be rejected, without installing
// a subscriber, restarting registration, or stealing the next healthy run.
func TestATSPIPrivateBusSubscriberCancelWhilePreviousDispatcherCleansUp(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	oldDone := make(chan struct{})
	b.eventCancel = func() {}
	b.eventDone = oldDone
	base, cancel := context.WithCancel(context.Background())
	ctx := &notifyEventWaitContext{Context: base, entered: make(chan struct{})}
	type answer struct {
		stream <-chan Event
		err    error
	}
	result := make(chan answer, 1)
	go func() { stream, err := b.Events(ctx, EventOptions{Buffer: 2}); result <- answer{stream, err} }()
	select {
	case <-ctx.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not reach prior-dispatcher cleanup wait")
	}
	cancel()
	select {
	case got := <-result:
		if got.stream != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled pending subscription accepted: channel=%v err=%v", got.stream, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled subscriber remained blocked during dispatcher cleanup")
	}
	b.eventsMu.Lock()
	queued := len(b.subscribers)
	b.eventsMu.Unlock()
	registry.mu.Lock()
	registered := len(registry.registered)
	registry.mu.Unlock()
	if queued != 0 || registered != 0 {
		t.Fatalf("canceled pending subscriber started provider: subscribers=%d registrations=%d", queued, registered)
	}
	b.finishEventDispatcher(oldDone)
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	stream, err := b.Events(healthyCtx, EventOptions{Buffer: 2})
	if err != nil || stream == nil {
		t.Fatalf("healthy subscriber cannot start after cleanup: stream=%v err=%v", stream, err)
	}
	healthyCancel()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("cancelled healthy subscription remained open")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("healthy subscription did not shut down")
	}
}
