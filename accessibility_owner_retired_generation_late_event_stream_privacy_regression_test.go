package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type eventStreamAfterGenerationRetiredBackend struct {
	*ownerLifecycleBackend
	entered chan struct{}
	private chan accessibility.Event
}

func (b *eventStreamAfterGenerationRetiredBackend) Events(ctx context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	close(b.entered)
	<-ctx.Done() // provider observes generation retirement, then wrongly returns events
	return b.private, nil
}

// A caller can remain live after the backend generation is retired. A late
// stream from the old generation must be denied, not mistaken for a successful
// subscription merely because the caller's own context remains live.
func TestAccessibilityOwnerRejectsPrivateEventStreamFromRetiredGeneration(t *testing.T) {
	backend := &eventStreamAfterGenerationRetiredBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(1, "events"), entered: make(chan struct{}), private: make(chan accessibility.Event, 1)}
	backend.private <- accessibility.Event{Kind: "private-change", Value: "former window secret"}
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	defer owner.Close()
	gen := owner.current
	type outcome struct {
		stream <-chan accessibility.Event
		err    error
	}
	results := make(chan outcome, 1)
	go func() {
		stream, err := owner.Events(context.Background(), accessibility.EventOptions{})
		results <- outcome{stream, err}
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was never entered")
	}
	gen.cancel()
	select {
	case got := <-results:
		if !errors.Is(got.err, accessibility.ErrDisconnected) || got.stream != nil {
			t.Fatalf("retired generation disclosed late private stream=%v error=%v", got.stream, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retired subscription failed to terminate")
	}
}

func TestAccessibilityPersistentBindingRejectsLateStreamFromRetiredGeneration(t *testing.T) {
	backend := &eventStreamAfterGenerationRetiredBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(1, "events"), entered: make(chan struct{}), private: make(chan accessibility.Event, 1)}
	backend.private <- accessibility.Event{Kind: "private-change", Value: "retired window secret"}
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	defer owner.Close()
	gen := owner.current
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := sub.beginBinding(gen)
	owner.eventSubs[sub] = struct{}{}
	type outcome struct {
		stream <-chan accessibility.Event
		err    error
	}
	results := make(chan outcome, 1)
	go func() {
		stream, stop, err := sub.openBinding(context.Background(), binding)
		if stop != nil {
			stop()
		}
		results <- outcome{stream, err}
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was never entered")
	}
	gen.cancel()
	select {
	case got := <-results:
		if !errors.Is(got.err, accessibility.ErrDisconnected) || got.stream != nil {
			t.Fatalf("retired persistent binding disclosed private stream=%v error=%v", got.stream, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retired persistent binding failed to terminate")
	}
}

// Verify the complete owner shutdown transaction, not just direct generation
// cancellation, when a source replies late with an already-buffered event.
func TestAccessibilityOwnerCloseRejectsLatePrivateEventStreamAndClosesProvider(t *testing.T) {
	backend := &eventStreamAfterGenerationRetiredBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(1, "events"), entered: make(chan struct{}), private: make(chan accessibility.Event, 1)}
	backend.private <- accessibility.Event{Kind: "private-change", Value: "closed-session secret"}
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	type outcome struct {
		stream <-chan accessibility.Event
		err    error
	}
	results := make(chan outcome, 1)
	go func() {
		stream, err := owner.Events(context.Background(), accessibility.EventOptions{})
		results <- outcome{stream, err}
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was not entered")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- owner.Close() }()
	select {
	case got := <-results:
		if !errors.Is(got.err, accessibility.ErrDisconnected) || got.stream != nil {
			t.Fatalf("shutdown leaked late event stream=%v err=%v", got.stream, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown failed to cancel in-flight provider subscription")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown stranded provider lease")
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("provider closed %d times, want once", backend.closeCalls.Load())
	}
}
