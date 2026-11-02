package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The event dispatcher never publishes a stale object identity after closure,
// and denial of an event must preserve the last authoritative generation.
func TestEventTransitionRejectsClosedOrDisconnectedBackend(t *testing.T) {
	event := Event{Kind: "object:property-change", Node: NodeID{BusName: "org.test.Application", ObjectPath: "/item", Generation: 1}}
	for _, tc := range []struct {
		name    string
		backend *dbusBackend
	}{
		{"nil", nil},
		{"closed", &dbusBackend{closed: true, generation: 7}},
		{"disconnected", &dbusBackend{disconnected: true, generation: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, active := tc.backend.prepareEventTransition(nil, event)
			if active {
				t.Fatal("closed event provider authorized input")
			}
			if tc.backend != nil && out.Node.Generation != tc.backend.generation {
				t.Fatalf("stale handle generation %d != %d", out.Node.Generation, tc.backend.generation)
			}
		})
	}
}

func TestUntrustedDestroySignalInvalidatesAllCachedHandles(t *testing.T) {
	for _, sig := range []*dbus.Signal{
		{Name: "org.a11y.atspi.Event.Window:Destroy", Sender: "org.test.Application", Path: ""},
		{Name: "org.a11y.atspi.Event.Window:Destroy", Sender: "", Path: "/node"},
	} {
		b := &dbusBackend{generation: 3, cacheItems: map[NodeID]cacheItem{{BusName: "org.test.Application", ObjectPath: "/other", Generation: 3}: {Name: "protected"}}, cacheApps: map[string]bool{"org.test.Application": true}}
		b.transitionObjectHandlesLocked(sig)
		if b.generation != 4 || b.cacheItems != nil || b.cacheApps != nil {
			t.Fatalf("unknown destroyed object left handles fresh: generation=%d items=%v apps=%v", b.generation, b.cacheItems, b.cacheApps)
		}
	}
}

func TestEventDeliveryTracksBackpressureWithoutCrossSubscriberLeaks(t *testing.T) {
	var nilBackend *dbusBackend
	nilBackend.deliverEvent(Event{Kind: "ignored"})
	slow := &eventSubscriber{out: make(chan Event, 1)}
	fast := &eventSubscriber{out: make(chan Event, 3)}
	b := &dbusBackend{subscribers: map[uint64]*eventSubscriber{1: slow, 2: fast}}
	b.deliverEvent(Event{Kind: "initial"})
	b.deliverEvent(Event{Kind: "second"})
	if slow.dropped != 1 || fast.dropped != 0 {
		t.Fatalf("incorrect overflow accounting slow=%d fast=%d", slow.dropped, fast.dropped)
	}
	first := <-slow.out
	if first.Kind != "initial" || first.Dropped != 0 {
		t.Fatalf("first event corrupted: %+v", first)
	}
	b.deliverEvent(Event{Kind: "after release"})
	last := <-slow.out
	if last.Kind != "after release" || last.Dropped != 1 || slow.dropped != 0 {
		t.Fatalf("lost events not reported after backpressure: %+v remaining=%d", last, slow.dropped)
	}
	for i, want := range []string{"initial", "second", "after release"} {
		got := <-fast.out
		if got.Kind != want || got.Dropped != 0 {
			t.Fatalf("fast subscriber event %d: %+v", i, got)
		}
	}
}

func TestEventSubscriberReservationWaitsForPreviousDispatcherCleanup(t *testing.T) {
	done := make(chan struct{})
	b := &dbusBackend{eventCancel: func() {}, eventDone: done}
	_, start, wait, err := b.reserveEventSubscriber(make(chan Event, 1))
	if err != nil || start || wait != done || len(b.subscribers) != 0 {
		t.Fatalf("overlapping dispatcher admitted: start=%v wait=%v err=%v", start, wait, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitErr := waitForEventRun(ctx, done); !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("canceled wait accepted dispatcher: %v", waitErr)
	}
	b.finishEventDispatcher(done)
	if waitErr := waitForEventRun(context.Background(), done); waitErr != nil {
		t.Fatalf("completed dispatcher wait: %v", waitErr)
	}
	id, start, wait, err := b.reserveEventSubscriber(make(chan Event, 1))
	if err != nil || !start || wait != nil || id == 0 || len(b.subscribers) != 1 {
		t.Fatalf("dispatcher not restarted after cleanup: id=%d start=%v wait=%v err=%v", id, start, wait, err)
	}
}

func TestEventShutdownCancelsSetupAndActiveDispatcherWithoutConnection(t *testing.T) {
	var nilBackend *dbusBackend
	nilBackend.stopEvents(nil)
	for _, tc := range []struct {
		name  string
		setup bool
	}{
		{"start", true}, {"dispatcher", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled := make(chan struct{})
			done := make(chan struct{})
			close(done)
			b := &dbusBackend{}
			if tc.setup {
				b.eventStarting = &eventStart{done: done}
				b.eventStartStop = func() { close(canceled) }
			} else {
				b.eventDone = done
				b.eventCancel = func() { close(canceled) }
			}
			b.stopEvents(nil)
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("event owner shutdown did not cancel")
			}
		})
	}
}

func TestEventRegistrationPreCanceledNeverCallsRegistryAndNullCleanupIsSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registry := &countingEventRegistrar{}
	registered, err := registerEventFamilies(ctx, registry, "org.test.Client", []string{"object:property-change"})
	if len(registered) != 0 || !errors.Is(err, context.Canceled) || registry.calls != 0 {
		t.Fatalf("pre-canceled registry used: registered=%v err=%v calls=%d", registered, err, registry.calls)
	}
	deregisterEvents(context.Background(), nil, []string{"object:property-change"})
	removeEventMatches(context.Background(), nil, nil)
}

type countingEventRegistrar struct{ calls int }

func (r *countingEventRegistrar) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	r.calls++
	return &dbus.Call{}
}
