package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Retired dispatchers must never restart registration, even if the underlying
// AT-SPI connection remains live during teardown.
func TestATSPIEventRetirementCannotRestartConnectedDispatcher(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	b.eventsMu.Lock()
	b.eventsRetired = true
	b.eventsMu.Unlock()
	if err := b.startEvents(context.Background(), true); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired dispatcher restarted against a live bus: %v", err)
	}
	if events, err := b.Events(context.Background(), EventOptions{}); events != nil || !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired dispatcher admitted subscriber: events=%v err=%v", events, err)
	}
	registry.mu.Lock()
	registrations := len(registry.registered)
	registry.mu.Unlock()
	if registrations != 0 {
		t.Fatalf("retired dispatcher registered %d event families", registrations)
	}
}

// A waiting subscriber can join only after the previous dispatcher publishes
// completion and clears its run identity. A fresh run then owns the stream.
func TestATSPIEventSubscriberRestartsAfterPriorDispatcherCompletion(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	oldDone := make(chan struct{})
	b.eventsMu.Lock()
	b.eventCancel = func() {}
	b.eventDone = oldDone
	b.eventsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notified := &notifyEventWaitContext{Context: ctx, entered: make(chan struct{})}
	type answer struct {
		events <-chan Event
		err    error
	}
	result := make(chan answer, 1)
	go func() { events, err := b.Events(notified, EventOptions{Buffer: 2}); result <- answer{events, err} }()
	select {
	case <-notified.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not wait on old dispatcher")
	}
	b.eventsMu.Lock()
	before := len(b.subscribers)
	b.eventsMu.Unlock()
	if before != 0 {
		t.Fatalf("waiting subscriber registered before cleanup: %d", before)
	}
	b.finishEventDispatcher(oldDone)
	select {
	case r := <-result:
		if r.err != nil || r.events == nil {
			t.Fatalf("subscriber not started after old run completed: events=%v err=%v", r.events, r.err)
		}
		registry.mu.Lock()
		registered := len(registry.registered)
		registry.mu.Unlock()
		if registered == 0 {
			t.Fatal("new subscription did not register event families")
		}
		cancel()
		select {
		case _, ok := <-r.events:
			if ok {
				t.Fatal("cancelled new subscription remained live")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("new subscription did not close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not restart after old run completed")
	}
}
