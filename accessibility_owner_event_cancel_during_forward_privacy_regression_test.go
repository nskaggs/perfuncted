package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

// The outgoing channel can be buffered. If cancellation occurs while the
// dispatcher is validating generation ownership, it must not publish an
// already-read private event after cancellation.
func TestAccessibilityOwnerDoesNotForwardBufferedPrivateEventAfterCallerCancellation(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(nil)
	owner.current = gen
	owner.generations[gen] = struct{}{}
	subscription := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 2), done: make(chan struct{})}
	binding := &accessibilityEventBinding{gen: gen, done: make(chan struct{})}
	source := make(chan accessibility.Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		next *accessibilityEventBinding
		err  error
	}
	finished := make(chan result, 1)
	owner.mu.Lock() // hold exactly the owner validation boundary
	go func() {
		var dropped uint64
		next, err := subscription.forwardGeneration(ctx, binding, source, func() {}, &dropped)
		finished <- result{next, err}
	}()
	sendDone := make(chan struct{})
	go func() {
		source <- accessibility.Event{Kind: "object:property-change", Property: "window-title", Value: "private bank statement"}
		close(sendDone)
	}()
	select {
	case <-sendDone: // source event consumed; forwardGeneration now attempts owner.isCurrent
	case <-time.After(2 * time.Second):
		owner.mu.Unlock()
		t.Fatal("dispatcher never consumed synthetic private event")
	}
	cancel()
	owner.mu.Unlock()
	select {
	case value := <-finished:
		if !errors.Is(value.err, context.Canceled) {
			t.Fatalf("dispatcher did not terminate on cancellation: %+v", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher remained blocked after cancellation")
	}
	select {
	case private := <-subscription.out:
		t.Fatalf("canceled caller received buffered private accessibility event: %+v", private)
	default:
	}
}
