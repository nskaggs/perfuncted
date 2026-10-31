package accessibility

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestATSPIRetireEventsForReopenStopsSetupAndDispatcherBeforeReturning(t *testing.T) {
	var setupCancels, dispatcherCancels atomic.Int32
	setupStopped := make(chan struct{})
	dispatcherStopped := make(chan struct{})
	setupDone := make(chan struct{})
	dispatcherDone := make(chan struct{})
	var onceSetup, onceDispatcher sync.Once
	b := &dbusBackend{
		eventStarting:  &eventStart{done: setupDone},
		eventStartStop: func() { setupCancels.Add(1); onceSetup.Do(func() { close(setupStopped) }) },
		eventCancel:    func() { dispatcherCancels.Add(1); onceDispatcher.Do(func() { close(dispatcherStopped) }) },
		eventDone:      dispatcherDone,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- b.RetireEventsForReopen(ctx) }()
	select {
	case <-setupStopped:
	case <-ctx.Done():
		t.Fatalf("setup cancellation not requested: %v", ctx.Err())
	}
	select {
	case <-dispatcherStopped:
	case <-ctx.Done():
		t.Fatalf("dispatcher cancellation not requested: %v", ctx.Err())
	}
	if !b.eventsRetired {
		t.Fatal("backend did not retire event admission before cancelling workers")
	}
	select {
	case err := <-finished:
		t.Fatalf("retirement returned before worker completion: %v", err)
	default:
	}
	close(setupDone)
	select {
	case err := <-finished:
		t.Fatalf("retirement returned with dispatcher active: %v", err)
	default:
	}
	close(dispatcherDone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("completed retirement failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("retirement did not join workers")
	}
	if setupCancels.Load() != 1 || dispatcherCancels.Load() != 1 {
		t.Fatalf("cancel counts setup=%d dispatcher=%d", setupCancels.Load(), dispatcherCancels.Load())
	}
}
func TestATSPIRetireEventsForReopenRespectsLifetimeCancellationAndPartialSetup(t *testing.T) {
	for _, tc := range []struct {
		name string
		init func(*dbusBackend)
	}{
		{"setup blocked", func(b *dbusBackend) { b.eventStarting = &eventStart{done: make(chan struct{})} }},
		{"dispatcher blocked", func(b *dbusBackend) { b.eventDone = make(chan struct{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{}
			tc.init(b)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := b.RetireEventsForReopen(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled retirement: %v", err)
			}
			if !b.eventsRetired {
				t.Fatal("canceled retirement admitted new subscriptions")
			}
		})
	}
	if err := (*dbusBackend)(nil).RetireEventsForReopen(context.Background()); err != nil {
		t.Fatalf("nil owner retirement: %v", err)
	}
	if err := (&dbusBackend{}).RetireEventsForReopen(nil); err == nil {
		t.Fatal("nil retirement context accepted")
	}
	b := &dbusBackend{}
	if err := b.RetireEventsForReopen(context.Background()); err != nil || !b.eventsRetired {
		t.Fatalf("idle retirement: state=%v error=%v", b.eventsRetired, err)
	}
}
func TestATSPIReopenRejectsInvalidContextAndUnavailablePrivateTarget(t *testing.T) {
	b := &dbusBackend{generation: 10, runtime: env.FromEnviron([]string{})}
	if _, err := b.Reopen(nil); err == nil || b.Generation() != 10 {
		t.Fatalf("nil reopen context mutated generation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Reopen(ctx); !errors.Is(err, context.Canceled) || b.Generation() != 10 {
		t.Fatalf("canceled reopen mutated generation: %v", err)
	}
	if _, err := (*dbusBackend)(nil).Reopen(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend reopened: %v", err)
	}
	if _, err := b.Reopen(context.Background()); !errors.Is(err, ErrDisconnected) || b.Generation() != 11 {
		t.Fatalf("missing address failed to retire prior epoch: err=%v generation=%d", err, b.Generation())
	}
}
