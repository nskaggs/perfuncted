package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityOwnerDropsObservedEventAfterGenerationRetires(t *testing.T) {
	oldBackend := newOwnerLifecycleBackend(401, "events")
	freshBackend := newOwnerLifecycleBackend(402, "events")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(oldBackend)
	oldGeneration := owner.current
	freshGeneration := newAccessibilityBackendGeneration(freshBackend)
	subscription := &accessibilityEventSubscription{
		owner: owner,
		out:   make(chan accessibility.Event, 1),
		done:  make(chan struct{}),
	}
	binding := subscription.beginBinding(oldGeneration)
	owner.mu.Lock()
	owner.eventSubs[subscription] = struct{}{}
	owner.mu.Unlock()

	source := make(chan accessibility.Event)
	type result struct {
		binding *accessibilityEventBinding
		err     error
	}
	resultCh := make(chan result, 1)
	cancelCalls := 0
	go func() {
		fresh, err := subscription.forwardGeneration(context.Background(), binding, source, func() { cancelCalls++ }, new(uint64))
		resultCh <- result{binding: fresh, err: err}
	}()

	// The unbuffered send proves the forwarding goroutine received the old
	// event. Holding the owner lock then makes its generation check observe the
	// replacement publication after the lock is released.
	owner.mu.Lock()
	source <- accessibility.Event{Kind: "private-old-generation-event"}
	owner.current = freshGeneration
	owner.generations[freshGeneration] = struct{}{}
	closeOld := owner.retireLocked(oldGeneration)
	owner.mu.Unlock()
	if closeOld {
		owner.closeGeneration(oldGeneration)
	}

	got := awaitOwnerSignal(t, resultCh, "forwarder to rebind after dropping stale event")
	if got.err != nil || got.binding == nil || got.binding.gen != freshGeneration {
		t.Fatalf("stale event transition returned binding=%+v err=%v", got.binding, got.err)
	}
	if len(subscription.out) != 0 {
		t.Fatalf("old-generation event escaped to subscriber: %+v", <-subscription.out)
	}
	if cancelCalls != 1 {
		t.Fatalf("retired event stream canceled %d times, want once", cancelCalls)
	}
	if oldBackend.closeCalls.Load() != 1 {
		t.Fatalf("retired backend closed %d times, want once", oldBackend.closeCalls.Load())
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("close owner after generation transition: %v", err)
	}
}

func TestAccessibilityOwnerRecordsClosedEventSourceBeforeRebinding(t *testing.T) {
	oldBackend := newOwnerLifecycleBackend(403, "events")
	freshBackend := newOwnerLifecycleBackend(404, "events")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(oldBackend)
	oldGeneration := owner.current
	freshGeneration := newAccessibilityBackendGeneration(freshBackend)
	subscription := &accessibilityEventSubscription{
		owner: owner,
		out:   make(chan accessibility.Event, 1),
		done:  make(chan struct{}),
	}
	binding := subscription.beginBinding(oldGeneration)
	owner.mu.Lock()
	owner.eventSubs[subscription] = struct{}{}
	owner.mu.Unlock()

	source := make(chan accessibility.Event)
	close(source)
	canceled := make(chan struct{})
	type result struct {
		binding *accessibilityEventBinding
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		fresh, err := subscription.forwardGeneration(context.Background(), binding, source, func() { close(canceled) }, new(uint64))
		resultCh <- result{binding: fresh, err: err}
	}()

	// The cancel signal occurs after the closed provider stream is observed and
	// before its diagnostic is recorded, so the owner lock fixes that ordering.
	owner.mu.Lock()
	awaitOwnerSignal(t, canceled, "closed event source cancellation")
	owner.current = freshGeneration
	owner.generations[freshGeneration] = struct{}{}
	closeOld := owner.retireLocked(oldGeneration)
	owner.mu.Unlock()
	if closeOld {
		owner.closeGeneration(oldGeneration)
	}

	got := awaitOwnerSignal(t, resultCh, "forwarder to rebind after provider stream closure")
	if got.err != nil || got.binding == nil || got.binding.gen != freshGeneration {
		t.Fatalf("closed source transition returned binding=%+v err=%v", got.binding, got.err)
	}
	owner.mu.Lock()
	limit := owner.eventLimits[subscription]
	owner.mu.Unlock()
	if want := "accessibility events unavailable: " + accessibility.ErrDisconnected.Error(); limit != want {
		t.Fatalf("closed provider stream diagnostic = %q, want %q", limit, want)
	}
	if len(subscription.out) != 0 {
		t.Fatalf("closed provider stream published an event: %+v", <-subscription.out)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("close owner after event source failure: %v", err)
	}
}

func TestAccessibilityOwnerRejectsReopenedBackendPublicationWithoutLiveOwner(t *testing.T) {
	for _, tc := range []struct {
		name         string
		session      *Session
		replaceOwner bool
		want         error
	}{
		{name: "missing session", want: ErrNilSession},
		{name: "closed session", session: &Session{closed: true}, want: ErrSessionClosed},
		{name: "retired source generation", session: &Session{ctx: context.Background()}, replaceOwner: true, want: accessibility.ErrDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := newAccessibilityBackendOwner(tc.session)
			oldBackend := newOwnerLifecycleBackend(405, "events", "reopen")
			owner.install(oldBackend)
			lease, err := owner.acquire()
			if err != nil {
				t.Fatalf("acquire source generation: %v", err)
			}
			if tc.replaceOwner {
				replacement := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(406, "events"))
				owner.mu.Lock()
				owner.current = replacement
				owner.generations[replacement] = struct{}{}
				owner.mu.Unlock()
			}

			_, _, err = owner.publishReopenedBackend(lease, newOwnerLifecycleBackend(407, "events"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("publish against unavailable owner error = %v, want %v", err, tc.want)
			}
			lease.release()
			if closeErr := owner.Close(); closeErr != nil {
				t.Fatalf("close owner after rejected publication: %v", closeErr)
			}
		})
	}
}
