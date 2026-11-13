package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A subscription can retain its binding for a generation that was already
// retired by another owner operation. Do not contact the stale provider or
// acknowledge an event stream; unblock waiters with the admission failure.
func TestAccessibilityEventBindingRejectsRetiredGenerationBeforeProviderAdmission(t *testing.T) {
	backend := newOwnerLifecycleBackend(301, "events")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	gen := owner.current
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := sub.beginBinding(gen)
	owner.mu.Lock()
	owner.eventSubs[sub] = struct{}{}
	closeNow := owner.retireLocked(gen)
	owner.mu.Unlock()
	if !closeNow {
		t.Fatal("idle generation should close on retirement")
	}
	owner.closeGeneration(gen)
	stream, cancel, err := sub.openBinding(context.Background(), binding)
	if stream != nil || cancel != nil || !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("retired generation accepted binding: stream=%v cancel=%v err=%v", stream, cancel, err)
	}
	if backend.eventCalls.Load() != 0 {
		t.Fatalf("retired provider received %d event requests", backend.eventCalls.Load())
	}
	select {
	case <-binding.done:
		if !errors.Is(binding.err, accessibility.ErrDisconnected) {
			t.Fatalf("binding acknowledged success: %v", binding.err)
		}
	default:
		t.Fatal("retired binding did not notify waiters")
	}
	owner.mu.Lock()
	limit := owner.eventLimits[sub]
	owner.mu.Unlock()
	if limit == "" {
		t.Fatal("retired binding failure not retained in event diagnostics")
	}
	sub.finishStream()
	if err := owner.Close(); err != nil {
		t.Fatalf("owner close: %v", err)
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("retired provider closed %d times", backend.closeCalls.Load())
	}
}
