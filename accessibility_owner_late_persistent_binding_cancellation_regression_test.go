package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A persistent event subscription can rebind onto a newly opened provider.
// If a provider disregards the canceled stream context while returning its
// event channel, the binding must not announce a successful active stream.
func TestAccessibilityPersistentEventRebindRejectsLateProviderStreamAfterCallerCancellation(t *testing.T) {
	backend := &ignoringCanceledOwnerEventBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(1, "events"),
		entered:               make(chan struct{}), release: make(chan struct{}), channel: make(chan accessibility.Event, 1),
	}
	backend.channel <- accessibility.Event{Kind: "private-change", Value: "customer balance"}
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	gen := owner.current
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := sub.beginBinding(gen)
	owner.eventSubs[sub] = struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		stream <-chan accessibility.Event
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		events, stop, err := sub.openBinding(ctx, binding)
		if stop != nil {
			stop()
		}
		done <- outcome{events, err}
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic provider did not receive subscribe request")
	}
	cancel()
	close(backend.release)
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.stream != nil {
			t.Fatalf("late canceled persistent binding exposed private stream=%v err=%v", got.stream, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled persistent binding never returned")
	}
	select {
	case <-binding.done:
		if !errors.Is(binding.err, context.Canceled) {
			t.Fatalf("binding acknowledged late cancellation as success: %v", binding.err)
		}
	case <-time.After(time.Second):
		t.Fatal("binding did not notify event waiters")
	}
}
