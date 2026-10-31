package accessibility

import (
	"context"
	"sync"
	"testing"
	"time"
)

type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestDispatcherCleanupStopsSubscriberWatcherWithoutCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan struct{})
	subscriberDone := make(chan struct{})
	runDone := make(chan struct{})
	out := make(chan Event, 1)
	backend := &dbusBackend{
		subscribers: map[uint64]*eventSubscriber{
			1: {out: out, done: subscriberDone},
		},
		eventDone: runDone,
	}
	watcherCtx := &observedDoneContext{Context: ctx, observed: observed}
	watcherDone := make(chan struct{})
	go func() {
		backend.watchSubscriber(watcherCtx, 1)
		close(watcherDone)
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("subscriber watcher did not enter its context wait")
	}

	backend.beginEventDispatcherCleanup(runDone)
	select {
	case <-watcherDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher cleanup left subscriber watcher waiting on caller cancellation")
	}
	select {
	case _, open := <-out:
		if open {
			t.Fatal("dispatcher cleanup left subscriber stream open")
		}
	default:
		t.Fatal("dispatcher cleanup left subscriber stream open")
	}
	select {
	case <-subscriberDone:
	default:
		t.Fatal("subscriber lifetime signal remained open after dispatcher cleanup")
	}
}
