package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
	"time"
)

// A core accessibility backend cannot authorize mutations by advertising a
// capability name alone; optional automation must be implemented explicitly.
func TestAccessibilityOwnerRejectsAutomationWhenBackendDoesNotImplementIt(t *testing.T) {
	backend := newOwnerLifecycleBackend(1, "automation", "applications")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(&coreOnlyAccessibilityBackend{Backend: backend})
	defer owner.Close()
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.example.NotOwned"}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"invoke action", func() error { return owner.InvokeAction(ctx, id, 0) }},
		{"invoke action by name", func() error { _, err := owner.InvokeActionByName(ctx, id, "activate"); return err }},
		{"default action", func() error { _, err := owner.InvokeDefaultAction(ctx, id); return err }},
		{"grab focus", func() error { return owner.GrabFocus(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, accessibility.ErrUnsupported) {
				t.Fatalf("automation without optional interface returned %v; want unsupported", err)
			}
		})
	}
	if _, err := owner.Applications(ctx); err != nil {
		t.Fatalf("core provider stopped working after rejected automation: %v", err)
	}
}

// A canceled accessibility query must not return sensitive content alongside
// context.Canceled, even when a provider ignores cancellation and completes
// after the caller abandoned authorization to receive those results.
func TestAccessibilityOwnerCanceledFindDoesNotReturnLateNodes(t *testing.T) {
	found := accessibility.Node{Name: "private document title", ID: accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/private"}}
	provider := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(1, "find"), findNodes: []accessibility.Node{found}, ignoreCancel: true, findEntered: make(chan struct{})}
	gate := make(chan struct{})
	provider.findRelease = gate
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		nodes []accessibility.Node
		err   error
	}
	output := make(chan result, 1)
	go func() {
		nodes, err := owner.Find(ctx, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		output <- result{nodes, err}
	}()
	<-provider.findEntered
	cancel()
	close(gate)
	select {
	case got := <-output:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("late node query error=%v", got.err)
		}
		if len(got.nodes) != 0 {
			t.Fatalf("canceled query leaked stale private nodes: %+v", got.nodes)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled provider did not return")
	}
}

// Closing the owner revokes both direct and generation-pinned admission. A
// subsequent invalidation or reopen must not touch the retired backend.
func TestAccessibilityOwnerClosedGenerationRefusesNewWorkAndInvalidation(t *testing.T) {
	backend := newOwnerLifecycleBackend(41, "applications")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	gen := lease.gen
	lease.release()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.acquire(); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner admitted a new lease: %v", err)
	}
	if _, err := owner.acquireGeneration(gen); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner admitted a retired generation: %v", err)
	}
	owner.Invalidate(accessibility.NodeID{BusName: "org.example.Forged"})
	if got := backend.generation.Load(); got != 41 {
		t.Fatalf("closed owner forwarded invalidation: generation=%d", got)
	}
	if err := owner.reopen(context.Background()); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner reopened a retired generation: %v", err)
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("backend closed %d times, want once", backend.closeCalls.Load())
	}
	if err := owner.Close(); err != nil || backend.closeCalls.Load() != 1 {
		t.Fatalf("repeated close changed teardown: err=%v closes=%d", err, backend.closeCalls.Load())
	}
}

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

// A finished caller subscription cannot be revived by a late backend
// generation change. In particular, no new event binding may be admitted
// after the caller has released the stream, even when a fresh provider exists.
func TestAccessibilityFinishedSubscriptionCannotRebindOnNewGeneration(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	old := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(901, "events"))
	fresh := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(902, "events"))
	owner.current = old
	owner.generations[old] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	owner.eventSubs[sub] = struct{}{}
	if sub.beginBinding(old) == nil {
		t.Fatal("initial binding not admitted")
	}
	owner.mu.Lock()
	closeNow := owner.retireLocked(old)
	owner.current = fresh
	owner.generations[fresh] = struct{}{}
	owner.mu.Unlock()
	if closeNow {
		owner.closeGeneration(old)
	}
	sub.finishStream()
	next, err := sub.nextBinding(context.Background(), old)
	if next != nil || !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("finished subscription was revived: binding=%v err=%v", next, err)
	}
	if sub.beginBinding(fresh) != nil {
		t.Fatal("closed stream permitted fresh binding")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNilAccessibilityBackendOwnerDoesNotAdvertiseBackend(t *testing.T) {
	var owner *accessibilityBackendOwner
	if owner.hasBackend() {
		t.Fatal("nil owner claimed accessible backend")
	}
}
