package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Cancellation arriving after the first lifetime check but just as a shared
// setup finishes must never allow a stale dispatcher to be used by a caller.
type cancellationAtCompletedEventSetup struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancellationAtCompletedEventSetup) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestEventWaitCompletionCannotOutraceCallerCancellation(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifetime := &cancellationAtCompletedEventSetup{Context: base, cancel: cancel}
	done := make(chan struct{})
	close(done)
	retry, err := waitForEventStart(lifetime, &eventStart{done: done}, nil, 0)
	if retry || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller accepted a completed dispatcher: retry=%v err=%v", retry, err)
	}
	if lifetime.checks != 2 {
		t.Fatalf("event setup checked caller lifetime %d times, want 2", lifetime.checks)
	}
}

// A disconnected backend without an event connection must not spawn a watcher
// or advance generation state merely because its lifecycle is inspected.
func TestEventDisconnectWatcherRequiresConnectedBackend(t *testing.T) {
	var missing *dbusBackend
	missing.watchDisconnect()
	missing.markDisconnected()
	empty := &dbusBackend{generation: 12, observationRevision: 7}
	empty.watchDisconnect()
	if empty.generation != 12 || empty.observationRevision != 7 || empty.disconnected {
		t.Fatalf("watching absent transport mutated backend state: %+v", empty)
	}
	empty.markDisconnected()
	if !empty.disconnected || empty.generation != 13 || empty.observationRevision != 8 {
		t.Fatalf("disconnect did not advance generation exactly once: generation=%d observation=%d disconnected=%v", empty.generation, empty.observationRevision, empty.disconnected)
	}
	empty.markDisconnected()
	if empty.generation != 13 || empty.observationRevision != 8 {
		t.Fatal("repeated disconnect advanced retired generation")
	}
}
