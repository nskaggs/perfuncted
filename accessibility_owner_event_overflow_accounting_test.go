package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

// The bounded subscriber queue must not block the provider's event stream.
// Any dropped events are accounted for in the next delivered event, then the
// counter resets instead of contaminating subsequent successful deliveries.
func TestAccessibilityEventForwarderReportsBackpressureLossOnNextEvent(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(nil)
	owner.current = gen
	owner.generations[gen] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := &accessibilityEventBinding{gen: gen, done: make(chan struct{})}
	source := make(chan accessibility.Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		var dropped uint64
		_, err := sub.forwardGeneration(ctx, binding, source, func() {}, &dropped)
		finished <- err
	}()
	send := func(kind string) {
		t.Helper()
		select {
		case source <- accessibility.Event{Kind: kind}:
		case <-time.After(time.Second):
			t.Fatal("provider blocked behind full subscriber queue")
		}
	}
	send("first")
	for i := 0; i < 32; i++ {
		send("discarded")
	}
	// Once a subsequent sender has been accepted, the previous event has
	// completed its forward attempt; the output buffer is still full here.
	send("barrier")
	first := <-sub.out
	if first.Kind != "first" || first.Dropped != 0 {
		t.Fatalf("unexpected first event %+v", first)
	}
	send("recovery")
	var next accessibility.Event
	select {
	case next = <-sub.out:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not resume after draining")
	}
	if next.Dropped == 0 {
		t.Fatalf("lost backpressure accounting: %+v", next)
	}
	// An already admitted event can still be in flight as the full queue is
	// drained. Deliver a few sequential events to establish that the loss
	// counter eventually resets rather than accumulating forever.
	for i := 0; i < 4; i++ {
		send("stable")
		select {
		case next = <-sub.out:
			if i == 3 && next.Dropped != 0 {
				t.Fatalf("drop count carried past repeated recovered deliveries: %+v", next)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive subsequent event")
		}
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("event loop terminated with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled event forwarder did not stop")
	}
}
