package sessionaccessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedOwnerCloseBackend struct {
	*ownerLifecycleBackend
	entered      chan struct{}
	release      chan struct{}
	closeFailure error
}

func (b *blockedOwnerCloseBackend) Close() error {
	close(b.entered)
	<-b.release
	_ = b.ownerLifecycleBackend.Close()
	return b.closeFailure
}

// Concurrent shutdown callers must share the same terminal result, wait for
// all held generation leases, and never call the provider's Close twice.
func TestAccessibilityOwnerConcurrentCloseWaitsAndPropagatesSingleProviderFailure(t *testing.T) {
	failure := errors.New("synthetic provider close refusal")
	backend := &blockedOwnerCloseBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(4, "events"), entered: make(chan struct{}), release: make(chan struct{}), closeFailure: failure}
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(backend)
	first := make(chan error, 1)
	go func() { first <- owner.Close() }()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider teardown did not start")
	}
	if owner.HasBackend() {
		t.Fatal("closing owner still offered backend to new callers")
	}
	if _, err := owner.acquire(); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closing owner allowed new lease: %v", err)
	}
	second := make(chan error, 1)
	go func() { second <- owner.Close() }()
	close(backend.release)
	for _, finished := range []<-chan error{first, second} {
		select {
		case err := <-finished:
			if !errors.Is(err, failure) {
				t.Fatalf("shared close did not propagate provider failure: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent owner close stranded waiting caller")
		}
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("provider close count=%d, want one", backend.closeCalls.Load())
	}
}

// Attackers can request millions of event buffer slots. Persistent streams
// must enforce the bounded capacity, and cancellation must retire the stream
// without requiring any event traffic from a provider.
func TestAccessibilityPersistentEventStreamBufferLimitAndCancellationCleanup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size, want int
	}{
		{"default", 0, 64},
		{"oversized", 1 << 20, 4096},
		{"minimum", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newOwnerLifecycleBackend(10, "events")
			owner := newAccessibilityBackendOwner(nil)
			owner.Install(backend)
			defer owner.Close()
			ctx, cancel := context.WithCancel(context.Background())
			stream, err := owner.EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: tc.size})
			if err != nil || stream == nil || cap(stream) != tc.want {
				cancel()
				t.Fatalf("buffer cap=%d want=%d err=%v", cap(stream), tc.want, err)
			}
			cancel()
			select {
			case _, ok := <-stream:
				if ok {
					t.Fatal("canceled stream still forwarded events")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("canceled persistent stream never closed")
			}
			owner.mu.Lock()
			active := len(owner.eventSubs)
			owner.mu.Unlock()
			if active != 0 {
				t.Fatalf("canceled stream retained %d subscriptions", active)
			}
		})
	}
}

func TestAccessibilityOwnerEventLimitBoundsProviderDiagnosticsAndClearsAfterRecovery(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event), done: make(chan struct{})}
	owner.eventSubs[sub] = struct{}{}
	owner.setEventLimit(sub, errors.New(strings.Repeat("private-error-", 100)))
	owner.mu.Lock()
	message := owner.eventLimits[sub]
	owner.mu.Unlock()
	if !strings.HasPrefix(message, "accessibility events unavailable: ") || len(message) > len("accessibility events unavailable: ")+256 {
		t.Fatalf("provider error diagnostics were not bounded: len=%d", len(message))
	}
	owner.setEventLimit(sub, nil)
	owner.mu.Lock()
	_, stillLimited := owner.eventLimits[sub]
	owner.mu.Unlock()
	if stillLimited {
		t.Fatal("recovered provider retained stale error diagnostics")
	}
}

// A core accessibility backend cannot authorize mutations by advertising a
// capability name alone; optional automation must be implemented explicitly.
func TestAccessibilityOwnerRejectsAutomationWhenBackendDoesNotImplementIt(t *testing.T) {
	backend := newOwnerLifecycleBackend(1, "automation", "applications")
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(&coreOnlyAccessibilityBackend{Backend: backend})
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
	session := newSessionForTesting(nil, nil, nil, nil, nil, provider)
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
	owner.Install(backend)
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
	if err := owner.Reopen(context.Background()); !errors.Is(err, accessibility.ErrDisconnected) {
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
	var owner *Owner
	if owner.HasBackend() {
		t.Fatal("nil owner claimed accessible backend")
	}
}

// A persistent event subscription can rebind onto a newly opened provider.
// If a provider disregards the canceled stream context while returning its
// event channel, the binding must not announce a successful active stream.
func TestAccessibilityPersistentEventRebindRejectsLateProviderStreamAfterCallerCancellation(t *testing.T) {
	backend := &ignoringCanceledOwnerEventBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(1, "events"),
		entered:               make(chan struct{}), release: make(chan struct{}), channel: make(chan accessibility.Event, 1),
	}
	backend.channel <- accessibility.Event{Kind: "private-change", Value: "customer balance"}
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(backend)
	gen := owner.current
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := sub.beginBinding(gen)
	owner.eventSubs[sub] = struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		stream <-chan accessibility.Event
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		events, stop, err := sub.openBinding(ctx, binding)
		if stop != nil {
			stop()
		}
		done <- outcome{events, err}
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic provider did not receive subscribe request")
	}
	cancel()
	close(backend.release)
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.stream != nil {
			t.Fatalf("late canceled persistent binding exposed private stream=%v err=%v", got.stream, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled persistent binding never returned")
	}
	select {
	case <-binding.done:
		if !errors.Is(binding.err, context.Canceled) {
			t.Fatalf("binding acknowledged late cancellation as success: %v", binding.err)
		}
	case <-time.After(time.Second):
		t.Fatal("binding did not notify event waiters")
	}
}

// Every accessibility query carries a caller context. An absent context
// must not be silently replaced with Background and disclose private nodes.
func TestAccessibilityOwnerQueriesRejectNilContextBeforeProviderDispatch(t *testing.T) {
	node := accessibility.Node{Name: "confidential desktop title", ID: accessibility.NodeID{BusName: "org.example.Private", ObjectPath: "/secret"}}
	base := newOwnerLifecycleBackend(3, "find", "focused", "at-point", "applications")
	base.apps = []accessibility.Application{{Name: "confidential program"}}
	backend := &ownerQueryBackend{ownerLifecycleBackend: base, findNodes: []accessibility.Node{node}, selected: node}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Applications", func() error {
			r, e := owner.Applications(nil)
			if len(r) > 0 {
				t.Errorf("nil context disclosed applications: %+v", r)
			}
			return e
		}},
		{"Find", func() error {
			r, e := owner.Find(nil, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
			if len(r) > 0 {
				t.Errorf("nil context disclosed nodes: %+v", r)
			}
			return e
		}},
		{"Focused", func() error {
			r, e := owner.Focused(nil, accessibility.SnapshotOptions{})
			if !reflect.DeepEqual(r, accessibility.Node{}) {
				t.Errorf("nil context disclosed focused node: %+v", r)
			}
			return e
		}},
		{"AtPoint", func() error {
			r, e := owner.AtPoint(nil, 1, 1)
			if !reflect.DeepEqual(r, accessibility.Node{}) {
				t.Errorf("nil context disclosed point node: %+v", r)
			}
			return e
		}},
		{"Snapshot", func() error {
			_, e := owner.Snapshot(nil, accessibility.NodeID{}, accessibility.SnapshotOptions{})
			return e
		}},
		{"FindApplication", func() error { _, e := owner.FindApplication(nil, accessibility.ApplicationFilter{}); return e }},
		{"FindApplicationFresh", func() error { _, e := owner.FindApplicationFresh(nil, accessibility.ApplicationFilter{}); return e }},
		{"ResolveWindow", func() error { _, e := owner.ResolveWindow(nil, accessibility.WindowTarget{}); return e }},
		{"ResolveWindowFresh", func() error { _, e := owner.ResolveWindowFresh(nil, accessibility.WindowTarget{}); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || !strings.Contains(err.Error(), "nil context") {
				t.Fatalf("nil accessibility query context not rejected: %v", err)
			}
		})
	}
	if backend.findCalls != 0 || backend.focusCalls != 0 || backend.pointCalls != 0 {
		t.Fatalf("nil context reached private provider: %+v", backend)
	}
	// The new boundary must not reject normal caller contexts.
	if _, err := owner.Find(context.Background(), accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{}); err != nil {
		t.Fatal(err)
	}
}

// A provider can allocate a replacement backend and then fail its final
// readiness checks. Reopen must close that partial replacement but preserve
// the previously serving generation and its capability to answer queries.
func TestAccessibilityReopenClosesPartiallyInitializedReplacementWithoutRetiringOriginal(t *testing.T) {
	denied := errors.New("synthetic replacement readiness verification failed")
	original := newOwnerLifecycleBackend(19, "applications", "reopen")
	var partials []*ownerLifecycleBackend
	original.reopen = func(context.Context) (accessibility.Backend, error) {
		partial := newOwnerLifecycleBackend(20, "applications")
		partials = append(partials, partial)
		return partial, denied
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil, original)
	owner := accessibilityOwnerForTest(t, session)
	t.Cleanup(func() { _ = session.Close() })
	for attempt := 0; attempt < 2; attempt++ {
		if err := session.accessibility.Reopen(context.Background()); !errors.Is(err, denied) {
			t.Fatalf("reopen %d hid provider readiness error: %v", attempt, err)
		}
		if !owner.HasBackend() || original.closeCalls.Load() != 0 {
			t.Fatalf("reopen %d retired usable original backend: available=%t closes=%d", attempt, owner.HasBackend(), original.closeCalls.Load())
		}
		if applications, err := session.accessibility.Applications(context.Background()); err != nil || len(applications) != 0 {
			t.Fatalf("reopen %d stopped original application queries: apps=%v err=%v", attempt, applications, err)
		}
	}
	if len(partials) != 2 {
		t.Fatalf("replacement attempts = %d, want two", len(partials))
	}
	for i, partial := range partials {
		if partial.closeCalls.Load() != 1 {
			t.Fatalf("failed replacement %d closed %d times, want once", i, partial.closeCalls.Load())
		}
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing owning session: %v", err)
	}
	if original.closeCalls.Load() != 1 {
		t.Fatalf("original generation closed %d times, want once", original.closeCalls.Load())
	}
}

// A subscription can retain its binding for a generation that was already
// retired by another owner operation. Do not contact the stale provider or
// acknowledge an event stream; unblock waiters with the admission failure.
func TestAccessibilityEventBindingRejectsRetiredGenerationBeforeProviderAdmission(t *testing.T) {
	backend := newOwnerLifecycleBackend(301, "events")
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(backend)
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

func TestAccessibilityOwnerAdvancesReopenedBackendThatReusesGeneration(t *testing.T) {
	old := newOwnerLifecycleBackend(44, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(44, "applications", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	if err := session.accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("reopen backend with reused generation: %v", err)
	}
	if got, want := fresh.Generation(), uint64(45); got != want {
		t.Fatalf("fresh backend generation = %d, want invalidated generation %d", got, want)
	}
	if got := owner.Generation(); got != fresh.Generation() || !owner.HasBackend() {
		t.Fatalf("owner published generation %d with backend available=%t; fresh generation=%d", got, owner.HasBackend(), fresh.Generation())
	}
	if got := old.closeCalls.Load(); got != 1 {
		t.Fatalf("retired backend close calls = %d, want one", got)
	}
	if got := fresh.closeCalls.Load(); got != 0 {
		t.Fatalf("published backend was closed %d times before session teardown", got)
	}
}

func TestAccessibilityOwnerRejectsFreshBackendAfterConcurrentOwnerClose(t *testing.T) {
	old := newOwnerLifecycleBackend(51, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(52, "applications", "reopen")
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)
	owner.mu.Lock()
	oldGeneration := owner.current
	owner.mu.Unlock()
	closeDone := make(chan error, 1)
	old.reopen = func(context.Context) (accessibility.Backend, error) {
		go func() { closeDone <- owner.Close() }()
		awaitOwnerSignal(t, oldGeneration.retiredCh, "owner closure during backend reopen")
		return fresh, nil
	}

	err := session.accessibility.Reopen(context.Background())
	if !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("reopen after owner closure = %v, want disconnected", err)
	}
	if closeErr := awaitOwnerSignal(t, closeDone, "owner close after reopen lease release"); closeErr != nil {
		t.Fatalf("owner.Close: %v", closeErr)
	}
	if owner.HasBackend() {
		t.Fatal("closed owner published a replacement backend")
	}
	if got := fresh.closeCalls.Load(); got != 1 {
		t.Fatalf("rejected replacement close calls = %d, want one", got)
	}
	if got := old.closeCalls.Load(); got != 1 {
		t.Fatalf("retired backend close calls = %d, want one", got)
	}
}

// The session can be retired independently of the caller's context. A
// provider that ignores cancellation must not publish a sensitive result to
// an operation whose session has already ended.
func TestAccessibilityOwnerSessionCancellationWithholdsLatePrivateResults(t *testing.T) {
	ctx, cancelSession := context.WithCancel(context.Background())
	release := make(chan struct{})
	backend := &ownerQueryBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(9, "find"),
		findNodes:             []accessibility.Node{{Name: "private document", ID: accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/private"}}},
		findEntered:           make(chan struct{}),
		findRelease:           release,
		ignoreCancel:          true,
	}
	session := &sessionCore{ctx: ctx}
	owner := newAccessibilityBackendOwner(session)
	owner.Install(backend)
	defer owner.Close()
	type outcome struct {
		nodes []accessibility.Node
		err   error
	}
	finished := make(chan outcome, 1)
	go func() {
		nodes, err := owner.Find(context.Background(), accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		finished <- outcome{nodes, err}
	}()
	<-backend.findEntered
	cancelSession()
	close(release)
	got := <-finished
	if got.nodes != nil || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("retired session returned sensitive nodes=%+v err=%v", got.nodes, got.err)
	}
}

// A provider that refuses to advance its generation cannot safely publish
// reused node handles after reconnect; the owner must abandon the old backend
// rather than silently exposing potentially stale accessibility identities.
type stubbornAccessibilityGeneration struct{ *ownerLifecycleBackend }

func (b *stubbornAccessibilityGeneration) Invalidate(accessibility.NodeID) {}

func TestAccessibilityOwnerRejectedStaleGenerationRevokesOwnerAndClosesBothBackends(t *testing.T) {
	old := newOwnerLifecycleBackend(37, "applications", "reopen")
	freshBase := newOwnerLifecycleBackend(37, "applications", "reopen")
	fresh := &stubbornAccessibilityGeneration{freshBase}
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)
	err := session.accessibility.Reopen(context.Background())
	if !errors.Is(err, accessibility.ErrStaleGeneration) {
		t.Fatalf("stale provider could reopen with reused node IDs: %v", err)
	}
	if owner.HasBackend() {
		t.Fatal("stale generation was published after failed validation")
	}
	if _, err := session.accessibility.Applications(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("abandoned owner allowed stale application query: %v", err)
	}
	if old.closeCalls.Load() != 1 || freshBase.closeCalls.Load() != 1 {
		t.Fatalf("old/fresh cleanup counts = %d/%d, want 1/1", old.closeCalls.Load(), freshBase.closeCalls.Load())
	}
	capability := session.Capability(CapabilityAccessibility)
	if capability.Available || !errors.Is(capability.Failure, ErrUnavailable) || !strings.Contains(capability.Failure.Error(), "generation") {
		t.Fatalf("stale provider still advertised as available: %+v", capability)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing abandoned session: %v", err)
	}
	if old.closeCalls.Load() != 1 || freshBase.closeCalls.Load() != 1 {
		t.Fatal("session close double-closed abandoned generations")
	}
}

func TestAccessibilityOwnerRejectsReopenWithNilOrPartiallyFailedProvider(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reopen  func(context.Context) (accessibility.Backend, error)
		failure error
	}{
		{"nil backend", func(context.Context) (accessibility.Backend, error) { return nil, nil }, accessibility.ErrDisconnected},
		{"provider error", func(context.Context) (accessibility.Backend, error) {
			return nil, errors.New("synthetic reconnect refused")
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := newOwnerLifecycleBackend(9, "applications", "reopen")
			old.reopen = tc.reopen
			session := newSessionForTesting(nil, nil, nil, nil, nil, old)
			t.Cleanup(func() { _ = session.Close() })
			err := session.accessibility.Reopen(context.Background())
			if err == nil {
				t.Fatal("invalid backend was accepted")
			}
			if tc.failure != nil && !errors.Is(err, tc.failure) {
				t.Fatalf("reconnect error %v, want %v", err, tc.failure)
			}
			if !accessibilityOwnerForTest(t, session).HasBackend() {
				t.Fatal("failed fresh backend invalidated still-usable prior generation")
			}
			if old.closeCalls.Load() != 0 {
				t.Fatal("old backend closed despite a failed replacement")
			}
			if _, err := session.accessibility.Applications(context.Background()); err != nil {
				t.Fatalf("old generation no longer usable: %v", err)
			}
		})
	}
}

type ignoringCanceledOwnerEventBackend struct {
	*ownerLifecycleBackend
	entered chan struct{}
	release chan struct{}
	channel chan accessibility.Event
}

func (b *ignoringCanceledOwnerEventBackend) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	close(b.entered)
	<-b.release
	return b.channel, nil
}

func TestAccessibilityOwnerDoesNotPublishLateEventStreamAfterCallerCancels(t *testing.T) {
	b := &ignoringCanceledOwnerEventBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(3, "events"), entered: make(chan struct{}), release: make(chan struct{}), channel: make(chan accessibility.Event, 1)}
	b.channel <- accessibility.Event{Kind: "private-notification"}
	session := newSessionForTesting(nil, nil, nil, nil, nil, b)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		stream <-chan accessibility.Event
		err    error
	}
	done := make(chan result, 1)
	go func() { stream, err := owner.Events(ctx, accessibility.EventOptions{}); done <- result{stream, err} }()
	<-b.entered
	cancel()
	close(b.release)
	select {
	case value := <-done:
		if !errors.Is(value.err, context.Canceled) || value.stream != nil {
			t.Fatalf("canceled subscription exposed private events: stream=%v err=%v", value.stream, value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled event subscription did not return")
	}
}

func TestAccessibilityOwnerRejectsPreCanceledPersistentEventRegistration(t *testing.T) {
	b := newOwnerLifecycleBackend(3, "events")
	session := newSessionForTesting(nil, nil, nil, nil, nil, b)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := owner.EventsAcrossReopens(ctx, accessibility.EventOptions{})
	if !errors.Is(err, context.Canceled) || stream != nil {
		t.Fatalf("canceled persistent registration returned stream=%v error=%v", stream, err)
	}
	if b.eventCalls.Load() != 0 {
		t.Fatalf("canceled registration subscribed backend: %d", b.eventCalls.Load())
	}
}

func TestAccessibilityOwnerNilAndRetiredLeaseBoundaries(t *testing.T) {
	var nilOwner *Owner
	if err := nilOwner.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	nilOwner.Invalidate(accessibility.NodeID{})
	if nilOwner.isCurrent(nil) {
		t.Fatal("nil owner considered current")
	}
	if _, err := nilOwner.acquireGeneration(nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner generation lease: %v", err)
	}
	if _, err := newAccessibilityBackendOwner(nil).acquireGeneration(nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil generation acquired: %v", err)
	}
	var nilLease *accessibilityBackendLease
	nilLease.release()
	var nilBinding *accessibilityEventBinding
	nilBinding.complete(accessibility.ErrDisconnected)
}

func TestAccessibilityOwnerRetiredLeaseClosesAfterLastOwnerReleases(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	if owner.retireLocked(nil) {
		t.Fatal("nil generation was retired")
	}
	backend := newOwnerLifecycleBackend(51, "applications")
	owner.Install(backend)
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.HasBackend() {
		t.Fatal("installed backend unavailable")
	}
	owner.mu.Lock()
	immediate := owner.retireLocked(lease.gen)
	repeated := owner.retireLocked(lease.gen)
	owner.mu.Unlock()
	if immediate || repeated {
		t.Fatalf("active or already retired generation closed prematurely: first=%v repeat=%v", immediate, repeated)
	}
	if owner.HasBackend() {
		t.Fatal("retired backend still advertised")
	}
	lease.release()
	select {
	case <-backend.closeDone:
	case <-time.After(time.Second):
		t.Fatal("retired generation not released when final lease exited")
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("retired backend closed %d times", backend.closeCalls.Load())
	}
	lease.release()
	if backend.closeCalls.Load() != 1 {
		t.Fatal("lease release was not idempotent")
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("closing retired owner: %v", err)
	}
}

func TestAccessibilityOwnerCanceledAndNilOperationContextNeverCallsBackend(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	backend := newOwnerLifecycleBackend(11, "applications")
	owner.Install(backend)
	defer owner.Close()
	calls := 0
	callback := func(accessibility.Backend, context.Context) error { calls++; return nil }
	var nilCtx context.Context
	if err := owner.withBackendContext(nilCtx, callback); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil context accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.withBackendContext(ctx, callback); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call succeeded: %v", err)
	}
	if calls != 0 {
		t.Fatalf("pre-canceled backend call executed %d times", calls)
	}
	if err := owner.withBackendContext(context.Background(), callback); err != nil || calls != 1 {
		t.Fatalf("authorized call result=%v calls=%d", err, calls)
	}
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	derived, done := lease.context(nilCtx)
	if derived.Err() != nil {
		t.Fatalf("nil parent was not normalized: %v", derived.Err())
	}
	done()
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatalf("context cleanup did not revoke lease: %v", derived.Err())
	}
	lease.release()
	if _, err := owner.EventsAcrossReopens(ctx, accessibility.EventOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled persistent event request was accepted: %v", err)
	}
	if err := owner.Reopen(nilCtx); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil reopen context accepted: %v", err)
	}
}

func TestAccessibilityEventBindingCancellationRetirementAndIdempotentCompletion(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(nil)
	owner.current = gen
	owner.generations[gen] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 2), done: make(chan struct{})}
	if got := sub.beginBinding(gen); got == nil {
		t.Fatal("binding was not created")
	}
	first := sub.beginBinding(gen)
	if reused := sub.beginBinding(gen); reused != first {
		t.Fatal("duplicate binding replaced current identity")
	}
	if err := sub.waitBinding(context.Background(), nil); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil binding accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sub.waitBinding(ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding wait: %v", err)
	}
	first.complete(errors.New("synthetic provider denied"))
	first.complete(nil)
	if err := sub.waitBinding(context.Background(), first); err == nil || !strings.Contains(err.Error(), "provider denied") {
		t.Fatalf("binding completion overwritten: %v", err)
	}
	sub.finishStream()
	sub.finishStream()
	if fresh := sub.beginBinding(newAccessibilityBackendGeneration(nil)); fresh != nil {
		t.Fatal("finished event stream opened new binding")
	}
	if err := sub.waitBinding(context.Background(), &accessibilityEventBinding{done: make(chan struct{})}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("finished subscription blocked or accepted: %v", err)
	}
	closeOwner := newAccessibilityBackendOwner(nil)
	if err := closeOwner.Close(); err != nil {
		t.Fatal(err)
	}
	if closeOwner.isCurrent(gen) {
		t.Fatal("closed owner admitted foreign generation")
	}
}

func TestAccessibilityReopenBindingFailsOnCanceledCallerAndRetiredGeneration(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	gen := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(73, "events"))
	owner.current = gen
	owner.generations[gen] = struct{}{}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sub.nextBinding(ctx, gen); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reopen wait: %v", err)
	}
	owner.mu.Lock()
	closeNow := owner.retireLocked(gen)
	owner.mu.Unlock()
	if closeNow {
		owner.closeGeneration(gen)
	}
	if _, err := sub.nextBinding(context.Background(), gen); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("retired unchanged generation rebound: %v", err)
	}
	owner.mu.Lock()
	owner.current = newAccessibilityBackendGeneration(newOwnerLifecycleBackend(74, "events"))
	owner.generations[owner.current] = struct{}{}
	owner.mu.Unlock()
	binding, err := sub.nextBinding(context.Background(), gen)
	if err != nil || binding == nil || binding.gen != owner.current {
		t.Fatalf("fresh generation binding=%v err=%v", binding, err)
	}
	sub.finishStream()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAccessibilityOwnerInstallAndEventAdmissionFailClosed(t *testing.T) {
	var absent *Owner
	if _, err := absent.EventsAcrossReopens(nil, accessibility.EventOptions{}); err == nil {
		t.Fatal("nil context was accepted for a wait-preserving event stream")
	}
	if _, err := absent.EventsAcrossReopens(context.Background(), accessibility.EventOptions{}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner event stream error = %v", err)
	}
	if _, err := absent.Events(nil, accessibility.EventOptions{}); err == nil {
		t.Fatal("nil context was accepted for a single-generation event stream")
	}
	if _, err := absent.Events(context.Background(), accessibility.EventOptions{}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner event source error = %v", err)
	}

	orphan := newOwnerLifecycleBackend(200, "events")
	absent.Install(orphan)
	awaitOwnerSignal(t, orphan.closeDone, "backend rejected by absent owner")
	if orphan.closeCalls.Load() != 1 {
		t.Fatalf("backend rejected by absent owner closed %d times", orphan.closeCalls.Load())
	}
	var typedNil *ownerLifecycleBackend
	newAccessibilityBackendOwner(nil).Install(typedNil)

	owner := newAccessibilityBackendOwner(nil)
	first := newOwnerLifecycleBackend(201, "events")
	rejected := newOwnerLifecycleBackend(202, "events")
	owner.Install(first)
	owner.Install(rejected)
	awaitOwnerSignal(t, rejected.closeDone, "duplicate backend rejected by owner")
	if rejected.closeCalls.Load() != 1 {
		t.Fatalf("duplicate backend closed %d times", rejected.closeCalls.Load())
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	late := newOwnerLifecycleBackend(203, "events")
	owner.Install(late)
	awaitOwnerSignal(t, late.closeDone, "backend rejected by closed owner")
	if first.closeCalls.Load() != 1 || late.closeCalls.Load() != 1 {
		t.Fatalf("backend closes after owner lifecycle: installed=%d late=%d", first.closeCalls.Load(), late.closeCalls.Load())
	}
}

func TestAccessibilityReopenRetireFailureLimitsEveryLiveStream(t *testing.T) {
	old := newOwnerLifecycleBackend(210, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(211, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.retireErr = errors.New(strings.Repeat("provider retirement failed ", 16))
	fresh.eventEntered = make(chan struct{})
	freshGate := make(chan struct{})
	fresh.eventGate = freshGate
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	var releaseOnce sync.Once
	releaseFreshEvents := func() { releaseOnce.Do(func() { close(freshGate) }) }
	t.Cleanup(func() {
		releaseFreshEvents()
		_ = session.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner := accessibilityOwnerForTest(t, session)
	streams := make([]<-chan accessibility.Event, 2)
	for i := range streams {
		stream, err := owner.EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 4})
		if err != nil {
			t.Fatalf("register event stream %d: %v", i, err)
		}
		streams[i] = stream
		fixture := awaitOwnerSignal(t, old.eventReady, "initial event stream")
		if fixture.events == nil {
			t.Fatal("initial event source returned no stream")
		}
	}

	reopen := make(chan error, 1)
	go func() { reopen <- session.accessibility.Reopen(ctx) }()
	awaitOwnerSignal(t, fresh.eventEntered, "fresh event source held during reopen")
	limits := 0
	for _, diagnostic := range session.Capability(CapabilityAccessibility).Diagnostics {
		if strings.HasPrefix(diagnostic, "accessibility events unavailable:") {
			limits++
			if len(diagnostic) > len("accessibility events unavailable: ")+256 {
				t.Fatalf("event failure diagnostic was not bounded: %d bytes", len(diagnostic))
			}
		}
	}
	if limits != len(streams) {
		t.Fatalf("retirement failure limited %d/%d live streams", limits, len(streams))
	}

	releaseFreshEvents()
	if err := awaitOwnerSignal(t, reopen, "reopen after nonfatal event-retirement error"); err != nil {
		t.Fatalf("reopen with recovered event transport: %v", err)
	}
	assertAccessibilityEventLimit(t, session, false)

	for range streams {
		fixture := awaitOwnerSignal(t, fresh.eventReady, "fresh event stream")
		if fixture.events == nil {
			t.Fatalf("fresh event binding failed: %+v", fixture)
		}
		fixture.events <- accessibility.Event{Kind: "recovered"}
	}
	for _, stream := range streams {
		select {
		case event, ok := <-stream:
			if !ok || event.Kind != "recovered" {
				t.Fatalf("rebound event stream delivered %+v, open=%t", event, ok)
			}
		case <-ctx.Done():
			t.Fatalf("rebound event did not reach a subscriber: %v", ctx.Err())
		}
	}
}

type accessibilityOwnerReopenWithLiveOldStream struct {
	accessibility.Backend
	generation uint64
	oldEvents  <-chan accessibility.Event
	oldEntered chan struct{}
	entered    sync.Once
	fresh      accessibility.Backend
}

func (b *accessibilityOwnerReopenWithLiveOldStream) Generation() uint64 {
	return b.generation
}

func (*accessibilityOwnerReopenWithLiveOldStream) Invalidate(accessibility.NodeID) {}

func (b *accessibilityOwnerReopenWithLiveOldStream) Reopen(ctx context.Context) (accessibility.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.fresh, nil
}

func (b *accessibilityOwnerReopenWithLiveOldStream) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.entered.Do(func() { close(b.oldEntered) })
	return b.oldEvents, nil
}

func TestAccessibilityOwnerRebindsWhenGenerationRetiresWithLiveEventStream(t *testing.T) {
	oldEvents := make(chan accessibility.Event)
	fresh := newOwnerLifecycleBackend(32, "applications", "events", "reopen")
	old := &accessibilityOwnerReopenWithLiveOldStream{
		Backend:    newOwnerLifecycleBackend(31, "applications", "events", "reopen"),
		generation: 31,
		oldEvents:  oldEvents,
		oldEntered: make(chan struct{}),
		fresh:      fresh,
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner := accessibilityOwnerForTest(t, session)
	stream, err := owner.EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	awaitOwnerSignal(t, old.oldEntered, "initial old-generation event source")

	owner.mu.Lock()
	var initialBinding *accessibilityEventBinding
	for sub := range owner.eventSubs {
		initialBinding = sub.binding
		break
	}
	owner.mu.Unlock()
	if initialBinding == nil {
		t.Fatal("initial generation has no event binding")
	}
	awaitOwnerSignal(t, initialBinding.done, "initial event binding completion")
	if initialBinding.err != nil {
		t.Fatalf("initial event binding failed: %v", initialBinding.err)
	}

	if err := session.accessibility.Reopen(ctx); err != nil {
		t.Fatalf("Reopen with an open old event stream: %v", err)
	}
	freshStream := awaitOwnerSignal(t, fresh.eventReady, "fresh-generation event source")
	if freshStream.events == nil {
		t.Fatalf("fresh event source returned no stream: %+v", freshStream)
	}
	freshStream.events <- accessibility.Event{Kind: "fresh-generation"}
	select {
	case event, ok := <-stream:
		if !ok || event.Kind != "fresh-generation" {
			t.Fatalf("event after generation retirement = %+v, open=%v", event, ok)
		}
	case <-ctx.Done():
		t.Fatalf("rebound stream did not receive fresh-generation event: %v", ctx.Err())
	}
}

// The wrapper exposes only the core Backend interface, even when its
// implementation supports optional capabilities. Optional dispatch must
// use the exposed interface, not assume a concrete provider has methods.
type coreOnlyAccessibilityBackend struct{ accessibility.Backend }

func TestAccessibilityOwnerOptionalProviderAbsenceFailsClosed(t *testing.T) {
	underlying := newOwnerLifecycleBackend(9, "applications", "find")
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(&coreOnlyAccessibilityBackend{Backend: underlying})
	defer owner.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"FindApplication", func() error { _, err := owner.FindApplication(ctx, accessibility.ApplicationFilter{}); return err }},
		{"FindApplicationFresh", func() error { _, err := owner.FindApplicationFresh(ctx, accessibility.ApplicationFilter{}); return err }},
		{"ResolveWindow", func() error { _, err := owner.ResolveWindow(ctx, accessibility.WindowTarget{}); return err }},
		{"ResolveWindowFresh", func() error { _, err := owner.ResolveWindowFresh(ctx, accessibility.WindowTarget{}); return err }},
		{"Events", func() error {
			stream, err := owner.Events(ctx, accessibility.EventOptions{})
			if stream != nil {
				t.Errorf("unsupported event source returned live stream")
			}
			return err
		}},
		{"Reopen", func() error { return owner.Reopen(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, accessibility.ErrUnsupported) {
				t.Fatalf("missing optional interface returned %v", err)
			}
		})
	}
	if gen := owner.Generation(); gen != 0 {
		t.Fatalf("unsupported generation source claimed generation %d", gen)
	}
	owner.Invalidate(accessibility.NodeID{BusName: "org.example.NotOwned"})
	if underlying.generation.Load() != 9 {
		t.Fatal("unsupported generation invalidation reached wrapped provider")
	}
	if _, err := owner.Applications(ctx); err != nil {
		t.Fatalf("core Backend operation incorrectly rejected: %v", err)
	}
}

func TestAccessibilityOwnerSingleGenerationEventSourceFailuresAndSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*ownerLifecycleBackend)
		want      error
	}{
		{"provider error", func(b *ownerLifecycleBackend) { b.eventErr = errors.New("synthetic provider stream failure") }, errors.New("synthetic provider stream failure")},
		{"nil stream", func(b *ownerLifecycleBackend) { b.eventNil = true }, accessibility.ErrDisconnected},
		{"valid stream", func(*ownerLifecycleBackend) {}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newOwnerLifecycleBackend(4, "events")
			tc.configure(b)
			owner := newAccessibilityBackendOwner(nil)
			owner.Install(b)
			defer owner.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stream, err := owner.Events(ctx, accessibility.EventOptions{})
			fixture := awaitOwnerSignal(t, b.eventReady, "owner event source startup")
			if tc.want != nil {
				if err == nil || stream != nil {
					t.Fatalf("failed subscription published stream=%v err=%v", stream, err)
				}
				if tc.name == "nil stream" && !errors.Is(err, accessibility.ErrDisconnected) {
					t.Fatalf("nil stream error=%v", err)
				}
			} else {
				if err != nil || stream == nil || fixture.events == nil {
					t.Fatalf("normal event source stream=%v err=%v fixture=%+v", stream, err, fixture)
				}
				fixture.events <- accessibility.Event{Kind: "synthetic-event"}
				select {
				case event := <-stream:
					if event.Kind != "synthetic-event" {
						t.Fatalf("wrong event %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("event not delivered")
				}
			}
		})
	}
}

type ownerQueryBackend struct {
	*ownerLifecycleBackend
	findRoot                          accessibility.NodeID
	findQuery                         accessibility.Query
	findOpts                          accessibility.SnapshotOptions
	focusedOpts                       accessibility.SnapshotOptions
	pointX, pointY                    int
	findCalls, focusCalls, pointCalls int
	findNodes                         []accessibility.Node
	selected                          accessibility.Node
	fail                              error
	findEntered                       chan struct{}
	findRelease                       <-chan struct{}
	ignoreCancel                      bool
}

func (b *ownerQueryBackend) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	if b.findEntered != nil {
		close(b.findEntered)
	}
	if b.findRelease != nil {
		<-b.findRelease
	}
	b.findCalls++
	b.findRoot = root
	b.findQuery = query
	b.findOpts = opts
	if !b.ignoreCancel {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return b.findNodes, b.fail
}
func (b *ownerQueryBackend) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.focusCalls++
	b.focusedOpts = opts
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.selected, b.fail
}
func (b *ownerQueryBackend) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	b.pointCalls++
	b.pointX = x
	b.pointY = y
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.selected, b.fail
}
func TestAccessibilityOwnerQueriesForwardExactInputsAndResults(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 5}
	found := accessibility.Node{ID: accessibility.NodeID{BusName: id.BusName, ObjectPath: "/save", Generation: 5}, Name: "Save", Role: "button"}
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(5, "find", "focused", "at-point"), findNodes: []accessibility.Node{found}, selected: found}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	query := accessibility.Query{Name: "Save", Role: "button"}
	opts := accessibility.SnapshotOptions{Fresh: true, MaxDepth: 2, MaxNodes: 5}
	nodes, err := owner.Find(context.Background(), id, query, opts)
	if err != nil || !reflect.DeepEqual(nodes, []accessibility.Node{found}) || backend.findCalls != 1 || backend.findRoot != id || !reflect.DeepEqual(backend.findQuery, query) || !reflect.DeepEqual(backend.findOpts, opts) {
		t.Fatalf("Find did not forward exact query: nodes=%+v err=%v backend=%+v", nodes, err, backend)
	}
	selected, err := owner.Focused(context.Background(), opts)
	if err != nil || !reflect.DeepEqual(selected, found) || backend.focusCalls != 1 || !reflect.DeepEqual(backend.focusedOpts, opts) {
		t.Fatalf("Focused result mismatch: %+v %v", selected, err)
	}
	hit, err := owner.AtPoint(context.Background(), -25, 900)
	if err != nil || !reflect.DeepEqual(hit, found) || backend.pointCalls != 1 || backend.pointX != -25 || backend.pointY != 900 {
		t.Fatalf("AtPoint result mismatch: %+v %v", hit, err)
	}
	operations := owner.SupportedOperations()
	if len(operations) != 3 || !reflect.DeepEqual(operations, []string{"find", "focused", "at-point"}) {
		t.Fatalf("owner operations incorrect: %v", operations)
	}
	operations[0] = "corrupt"
	if got := owner.SupportedOperations(); got[0] != "find" {
		t.Fatalf("caller mutated backend operations: %v", got)
	}
}
func TestAccessibilityOwnerQueriesPreserveErrorsAndRejectClosedOrCanceledSessions(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 4}
	providerErr := errors.New("synthetic AT-SPI scope read failed")
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(4), fail: providerErr}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	owner := accessibilityOwnerForTest(t, session)
	ctx := context.Background()
	if _, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{}); !errors.Is(err, providerErr) {
		t.Fatalf("Find provider failure=%v", err)
	}
	if _, err := owner.Focused(ctx, accessibility.SnapshotOptions{}); !errors.Is(err, providerErr) {
		t.Fatalf("Focused provider failure=%v", err)
	}
	if _, err := owner.AtPoint(ctx, 0, 0); !errors.Is(err, providerErr) {
		t.Fatalf("AtPoint provider failure=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, op := range []struct {
		name string
		call func(context.Context) error
	}{
		{"Find", func(ctx context.Context) error {
			_, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"Focused", func(ctx context.Context) error {
			_, err := owner.Focused(ctx, accessibility.SnapshotOptions{})
			return err
		}},
		{"AtPoint", func(ctx context.Context) error { _, err := owner.AtPoint(ctx, 4, 5); return err }},
	} {
		t.Run(op.name, func(t *testing.T) {
			if err := op.call(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled query reported %v", err)
			}
		})
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		name string
		call func() error
	}{
		{"Find", func() error {
			_, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"Focused", func() error { _, err := owner.Focused(ctx, accessibility.SnapshotOptions{}); return err }},
		{"AtPoint", func() error { _, err := owner.AtPoint(ctx, 4, 5); return err }},
	} {
		t.Run("closed_"+op.name, func(t *testing.T) {
			if err := op.call(); !errors.Is(err, accessibility.ErrDisconnected) {
				t.Fatalf("closed backend allowed query: %v", err)
			}
		})
	}
	if got := owner.SupportedOperations(); len(got) != 0 {
		t.Fatalf("closed owner exposed operations: %v", got)
	}
}

func TestAccessibilityOwnerRejectsLateSuccessAfterCallerCancellation(t *testing.T) {
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(3), findEntered: make(chan struct{}), ignoreCancel: true}
	release := make(chan struct{})
	backend.findRelease = release
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := owner.Find(ctx, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		done <- err
	}()
	<-backend.findEntered
	cancel()
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("owner reported late successful query after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked query never completed")
	}
}

type gatedDiagnosticsBackend struct {
	*ownerLifecycleBackend
	entered chan struct{}
	release <-chan struct{}
}

func (b *gatedDiagnosticsBackend) Diagnostics() []string {
	close(b.entered)
	<-b.release
	return []string{"fresh diagnostics"}
}

func TestAccessibilityOwnerDoesNotPublishRefreshAfterClose(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	owner := accessibilityOwnerForTest(t, session)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	backend := &gatedDiagnosticsBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(1, "applications"),
		entered:               make(chan struct{}),
		release:               release,
	}
	owner.Install(backend)
	t.Cleanup(func() {
		unblock()
		_ = session.Close()
	})

	session.capabilitiesMu.Lock()
	session.capabilities[CapabilityAccessibility] = CapabilityStatus{
		Capability: CapabilityAccessibility,
		Available:  false,
		Backend:    "published-before-close",
	}
	session.capabilitiesMu.Unlock()

	owner.mu.Lock()
	gen := owner.current
	owner.mu.Unlock()
	refreshDone := make(chan struct{})
	go func() {
		owner.refreshCapabilities()
		close(refreshDone)
	}()
	awaitOwnerSignal(t, backend.entered, "capability diagnostics")

	closeDone := make(chan error, 1)
	go func() { closeDone <- owner.Close() }()
	awaitOwnerSignal(t, gen.retiredCh, "backend generation retirement")
	unblock()
	awaitOwnerSignal(t, refreshDone, "capability refresh completion")
	if err := awaitOwnerSignal(t, closeDone, "accessibility owner close"); err != nil {
		t.Fatalf("owner.Close: %v", err)
	}
	if !backend.closed.Load() || backend.closeCalls.Load() != 1 {
		t.Fatalf("backend close state = (%v, %d calls), want (closed, 1 call)", backend.closed.Load(), backend.closeCalls.Load())
	}

	status := session.Capability(CapabilityAccessibility)
	if status.Available || status.Backend != "published-before-close" || len(status.Diagnostics) != 0 {
		t.Fatalf("capability after close = %+v, want pre-close status unchanged", status)
	}
}

type cancelOnSuccessfulEventRetirement struct {
	*ownerLifecycleBackend
	cancel context.CancelFunc
}

func (b *cancelOnSuccessfulEventRetirement) RetireEventsForReopen(context.Context) error {
	b.cancel()
	return nil
}

func TestAccessibilityReopenCancellationDuringRetirementClosesFreshBackend(t *testing.T) {
	old := newOwnerLifecycleBackend(40, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(41, "applications", "reopen")
	old.retireEntered = make(chan struct{})
	old.retireGate = make(chan struct{})
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reopened := make(chan error, 1)
	go func() { reopened <- session.accessibility.Reopen(ctx) }()
	awaitOwnerSignal(t, old.retireEntered, "event retirement during reopen")
	cancel()
	if err := awaitOwnerSignal(t, reopened, "canceled reopen after event retirement"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen after caller cancellation returned %v, want context.Canceled", err)
	}
	if !owner.HasBackend() || owner.Generation() != 40 || old.closeCalls.Load() != 0 {
		t.Fatalf("canceled reopen replaced or retired the current backend: available=%t generation=%d old closes=%d", owner.HasBackend(), owner.Generation(), old.closeCalls.Load())
	}
	if fresh.closeCalls.Load() != 1 {
		t.Fatalf("canceled reopen closed fresh backend %d times, want once", fresh.closeCalls.Load())
	}
	if _, err := session.accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("current backend stopped serving applications after canceled reopen: %v", err)
	}
}

func TestAccessibilityReopenChecksCancellationAfterSuccessfulRetirement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &cancelOnSuccessfulEventRetirement{
		ownerLifecycleBackend: newOwnerLifecycleBackend(42, "applications", "reopen"),
		cancel:                cancel,
	}
	fresh := newOwnerLifecycleBackend(43, "applications", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	if err := session.accessibility.Reopen(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen after successful retirement canceled its caller with %v, want context.Canceled", err)
	}
	if !owner.HasBackend() || owner.Generation() != 42 || old.closeCalls.Load() != 0 {
		t.Fatalf("canceled reopen replaced or retired the current backend: available=%t generation=%d old closes=%d", owner.HasBackend(), owner.Generation(), old.closeCalls.Load())
	}
	if fresh.closeCalls.Load() != 1 {
		t.Fatalf("canceled reopen closed fresh backend %d times, want once", fresh.closeCalls.Load())
	}
	if _, err := session.accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("current backend stopped serving applications after canceled reopen: %v", err)
	}
}

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
	owner.Install(backend)
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
	owner.Install(backend)
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
	owner.Install(backend)
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

func TestAccessibilityOwnerDropsObservedEventAfterGenerationRetires(t *testing.T) {
	oldBackend := newOwnerLifecycleBackend(401, "events")
	freshBackend := newOwnerLifecycleBackend(402, "events")
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(oldBackend)
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
	owner.Install(oldBackend)
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
		session      *sessionCore
		replaceOwner bool
		want         error
	}{
		{name: "missing session", want: ErrNilSession},
		{name: "closed session", session: &sessionCore{closed: true}, want: ErrSessionClosed},
		{name: "retired source generation", session: &sessionCore{ctx: context.Background()}, replaceOwner: true, want: accessibility.ErrDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := newAccessibilityBackendOwner(tc.session)
			oldBackend := newOwnerLifecycleBackend(405, "events", "reopen")
			owner.Install(oldBackend)
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

type replacingOwnerEventSource struct {
	*ownerLifecycleBackend
	onEvents func()
	private  chan accessibility.Event
}

func (b *replacingOwnerEventSource) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.onEvents()
	return b.private, nil
}

// A provider can return a valid buffered stream while the owner's current
// generation changes. Even before the old generation cancellation propagates,
// an old stream is no longer authorized to expose private events.
func TestAccessibilityBindingRejectsStreamAfterOwnerIdentitySwap(t *testing.T) {
	backend := &replacingOwnerEventSource{ownerLifecycleBackend: newOwnerLifecycleBackend(701, "events"), private: make(chan accessibility.Event, 1)}
	backend.private <- accessibility.Event{Kind: "private-window-change", Value: "private data"}
	owner := newAccessibilityBackendOwner(nil)
	owner.Install(backend)
	defer owner.Close()
	oldGen := owner.current
	newGen := newAccessibilityBackendGeneration(newOwnerLifecycleBackend(702, "events"))
	backend.onEvents = func() {
		owner.mu.Lock()
		owner.current = newGen
		owner.generations[newGen] = struct{}{}
		owner.mu.Unlock()
	}
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event, 1), done: make(chan struct{})}
	binding := sub.beginBinding(oldGen)
	owner.eventSubs[sub] = struct{}{}
	events, cancel, err := sub.openBinding(context.Background(), binding)
	if cancel != nil {
		cancel()
	}
	if events != nil || !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("stale stream admitted: stream=%v err=%v", events, err)
	}
	if len(sub.out) != 0 {
		t.Fatal("retired generation published private events")
	}
	if err := sub.waitBinding(context.Background(), binding); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("binding waiter incorrectly authorized: %v", err)
	}
}

type ownerLifecycleBackend struct {
	operations []string
	generation atomic.Uint64
	closed     atomic.Bool
	usedClosed atomic.Bool
	closeOnce  sync.Once
	closeCalls atomic.Int32
	closeErr   error
	closeDone  chan struct{}

	appsEntered chan struct{}
	appsGate    <-chan struct{}
	appsOnce    sync.Once
	apps        []accessibility.Application

	eventReady       chan ownerEventFixture
	eventEntered     chan struct{}
	eventEnteredOnce sync.Once
	eventGate        <-chan struct{}
	eventErr         error
	eventNil         bool
	eventCalls       atomic.Int32
	eventMu          sync.Mutex
	eventCancels     []context.CancelFunc
	eventClosers     []func()
	eventRetired     bool
	retireEntered    chan struct{}
	retireOnce       sync.Once
	retireGate       <-chan struct{}
	retireErr        error
	retireCalls      atomic.Int32
	reopen           func(context.Context) (accessibility.Backend, error)
}

type ownerEventFixture struct {
	events    chan accessibility.Event
	close     func()
	err       error
	nilStream bool
}

func newOwnerLifecycleBackend(generation uint64, operations ...string) *ownerLifecycleBackend {
	backend := &ownerLifecycleBackend{
		operations: operations,
		closeDone:  make(chan struct{}),
		eventReady: make(chan ownerEventFixture, 8),
	}
	backend.generation.Store(generation)
	return backend
}

func (b *ownerLifecycleBackend) SupportedOperations() []string {
	return append([]string(nil), b.operations...)
}

func (b *ownerLifecycleBackend) Applications(context.Context) ([]accessibility.Application, error) {
	if b.closed.Load() {
		b.usedClosed.Store(true)
	}
	if b.appsEntered != nil {
		b.appsOnce.Do(func() { close(b.appsEntered) })
	}
	if b.appsGate != nil {
		<-b.appsGate
	}
	if b.closed.Load() {
		b.usedClosed.Store(true)
	}
	return append([]accessibility.Application(nil), b.apps...), nil
}

func (*ownerLifecycleBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	return accessibility.Snapshot{}, nil
}

func (*ownerLifecycleBackend) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return nil, nil
}

func (*ownerLifecycleBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{}, nil
}

func (*ownerLifecycleBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{}, nil
}

func (b *ownerLifecycleBackend) Close() error {
	b.closeCalls.Add(1)
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.closeDone)
	})
	return b.closeErr
}

func (b *ownerLifecycleBackend) Generation() uint64              { return b.generation.Load() }
func (b *ownerLifecycleBackend) Invalidate(accessibility.NodeID) { b.generation.Add(1) }

func (b *ownerLifecycleBackend) Reopen(ctx context.Context) (accessibility.Backend, error) {
	if b.reopen == nil {
		return nil, accessibility.ErrUnsupported
	}
	return b.reopen(ctx)
}

func (b *ownerLifecycleBackend) Events(ctx context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.eventCalls.Add(1)
	if b.eventEntered != nil {
		b.eventEnteredOnce.Do(func() { close(b.eventEntered) })
	}
	eventCtx, cancel := context.WithCancel(ctx)
	b.eventMu.Lock()
	if b.eventRetired {
		b.eventMu.Unlock()
		cancel()
		b.eventReady <- ownerEventFixture{err: accessibility.ErrDisconnected}
		return nil, accessibility.ErrDisconnected
	}
	b.eventCancels = append(b.eventCancels, cancel)
	eventGate := b.eventGate
	eventErr := b.eventErr
	eventNil := b.eventNil
	b.eventMu.Unlock()
	if eventGate != nil {
		select {
		case <-eventGate:
		case <-eventCtx.Done():
			err := eventCtx.Err()
			cancel()
			b.eventReady <- ownerEventFixture{err: err}
			return nil, err
		}
	}
	if err := eventCtx.Err(); err != nil {
		cancel()
		b.eventReady <- ownerEventFixture{err: err}
		return nil, err
	}
	if eventErr != nil {
		cancel()
		b.eventReady <- ownerEventFixture{err: eventErr}
		return nil, eventErr
	}
	if eventNil {
		cancel()
		b.eventReady <- ownerEventFixture{nilStream: true}
		return nil, nil
	}
	stream := make(chan accessibility.Event, 4)
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(func() { close(stream) }) }
	b.eventMu.Lock()
	b.eventClosers = append(b.eventClosers, closeStream)
	b.eventMu.Unlock()
	b.eventReady <- ownerEventFixture{events: stream, close: closeStream}
	go func() {
		<-eventCtx.Done()
		closeStream()
	}()
	return stream, nil
}

func (b *ownerLifecycleBackend) RetireEventsForReopen(ctx context.Context) error {
	b.retireCalls.Add(1)
	b.eventMu.Lock()
	b.eventRetired = true
	cancels := append([]context.CancelFunc(nil), b.eventCancels...)
	closers := append([]func(){}, b.eventClosers...)
	gate := b.retireGate
	b.eventMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, closeStream := range closers {
		closeStream()
	}
	if b.retireEntered != nil {
		b.retireOnce.Do(func() { close(b.retireEntered) })
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.Join(b.retireErr, ctx.Err())
}

func awaitOwnerSignal[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func accessibilityOwnerForTest(t *testing.T, s *sessionCore) *Owner { t.Helper(); return s.owner }

func TestAccessibilityOwnerDefersRetiredBackendCloseUntilCallsRelease(t *testing.T) {
	old := newOwnerLifecycleBackend(4, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	fresh := newOwnerLifecycleBackend(5, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	old.appsEntered = make(chan struct{})
	appsGate := make(chan struct{})
	var releaseOnce sync.Once
	releaseOldCall := func() { releaseOnce.Do(func() { close(appsGate) }) }
	old.appsGate = appsGate
	old.apps = []accessibility.Application{{Name: "old"}}
	fresh.apps = []accessibility.Application{{Name: "fresh"}}
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() {
		releaseOldCall()
		_ = session.Close()
	})

	oldCall := make(chan error, 1)
	go func() {
		_, err := session.accessibility.Applications(context.Background())
		oldCall <- err
	}()
	awaitOwnerSignal(t, old.appsEntered, "old accessibility call")

	if err := session.accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	apps, err := session.accessibility.Applications(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "fresh" {
		t.Fatalf("Applications after reopen = (%+v, %v), want fresh backend", apps, err)
	}
	if old.closed.Load() {
		t.Fatal("retired backend closed while an accessibility call still held its lease")
	}
	owner := accessibilityOwnerForTest(t, session)
	owner.mu.Lock()
	retainedWhilePinned := len(owner.generations)
	owner.mu.Unlock()
	if retainedWhilePinned != 2 {
		t.Fatalf("retained generations while old call is pinned = %d, want 2", retainedWhilePinned)
	}

	releaseOldCall()
	if err := awaitOwnerSignal(t, oldCall, "in-flight accessibility call"); err != nil {
		t.Fatalf("in-flight Applications: %v", err)
	}
	awaitOwnerSignal(t, old.closeDone, "retired backend close")
	owner.mu.Lock()
	retainedAfterRelease := len(owner.generations)
	owner.mu.Unlock()
	if retainedAfterRelease != 1 {
		t.Fatalf("retained generations after old call released = %d, want 1", retainedAfterRelease)
	}
	if old.usedClosed.Load() {
		t.Fatal("accessibility call used the backend after Close")
	}
	if session.accessibility.Generation() <= old.Generation() {
		t.Fatalf("generation after reopen = %d, old generation = %d", session.accessibility.Generation(), old.Generation())
	}
}

func TestAccessibilityWaitRebindFailureCompletesAndAllowsLaterReopen(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ownerLifecycleBackend)
		wantError bool
	}{
		{name: "error", configure: func(backend *ownerLifecycleBackend) { backend.eventErr = errors.New("event registration failed") }, wantError: true},
		{name: "nil stream", configure: func(backend *ownerLifecycleBackend) { backend.eventNil = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testAccessibilityWaitRebindFailure(t, test.configure, test.wantError)
		})
	}
}

func testAccessibilityWaitRebindFailure(t *testing.T, configure func(*ownerLifecycleBackend), wantError bool) {
	t.Helper()
	old := newOwnerLifecycleBackend(20, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	failed := newOwnerLifecycleBackend(21, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	recovered := newOwnerLifecycleBackend(22, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	configure(failed)
	old.reopen = func(context.Context) (accessibility.Backend, error) { return failed, nil }
	failed.reopen = func(context.Context) (accessibility.Backend, error) { return recovered, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	oldStream := awaitOwnerSignal(t, old.eventReady, "initial event bind")
	if oldStream.events == nil {
		t.Fatal("initial event bind returned no stream")
	}

	if err := session.accessibility.Reopen(ctx); err != nil {
		t.Fatalf("Reopen with optional event failure: %v", err)
	}
	failedAttempt := awaitOwnerSignal(t, failed.eventReady, "failed event bind result")
	assertOwnerEventFailure(t, failedAttempt, failed, wantError)
	assertAccessibilityEventLimit(t, session, true)

	if err := session.accessibility.Reopen(ctx); err != nil {
		t.Fatalf("Reopen after failed event bind: %v", err)
	}
	recoveredStream := awaitOwnerSignal(t, recovered.eventReady, "recovered event bind")
	if recoveredStream.events == nil {
		t.Fatalf("recovered event stream is nil: %+v", recoveredStream)
	}
	assertAccessibilityEventLimit(t, session, false)

	recoveredStream.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	select {
	case event, ok := <-stream:
		if !ok || event.Kind != "state-changed" {
			t.Fatalf("recovered event = %+v, open=%v", event, ok)
		}
	case <-ctx.Done():
		t.Fatalf("wait stream did not receive recovered event: %v", ctx.Err())
	}
}

func assertOwnerEventFailure(t *testing.T, attempt ownerEventFixture, backend *ownerLifecycleBackend, wantError bool) {
	t.Helper()
	if wantError {
		if !errors.Is(attempt.err, backend.eventErr) {
			t.Fatalf("failed event bind error = %v, want %v", attempt.err, backend.eventErr)
		}
		return
	}
	if !attempt.nilStream {
		t.Fatalf("failed event bind = %+v, want nil stream", attempt)
	}
}

func assertAccessibilityEventLimit(t *testing.T, session *sessionCore, wantLimit bool) {
	t.Helper()
	diagnostics := session.Capability(CapabilityAccessibility).Diagnostics
	if got := containsDiagnostic(diagnostics, "accessibility events unavailable"); got != wantLimit {
		t.Fatalf("event limit present = %v, want %v; diagnostics = %v", got, wantLimit, diagnostics)
	}
}

func TestAccessibilityWaitRebindSurvivesBrokenStream(t *testing.T) {
	old := newOwnerLifecycleBackend(30, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(31, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	oldStream := awaitOwnerSignal(t, old.eventReady, "initial event bind")
	oldStream.close()

	if err := session.accessibility.Reopen(ctx); err != nil {
		t.Fatalf("Reopen after broken stream: %v", err)
	}
	freshStream := awaitOwnerSignal(t, fresh.eventReady, "event bind after broken stream")
	freshStream.events <- accessibility.Event{Kind: "state-changed", Property: "visible"}
	select {
	case event, ok := <-stream:
		if !ok || event.Kind != "state-changed" {
			t.Fatalf("event after broken stream = %+v, open=%v", event, ok)
		}
	case <-ctx.Done():
		t.Fatalf("wait stream did not recover after broken stream: %v", ctx.Err())
	}
}

func TestAccessibilityReopenCancellationDuringBindAllowsLaterReopen(t *testing.T) {
	old := newOwnerLifecycleBackend(40, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	blocked := newOwnerLifecycleBackend(41, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	recovered := newOwnerLifecycleBackend(42, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	eventEntered := make(chan struct{})
	blocked.eventEntered = eventEntered
	blocked.eventGate = make(chan struct{})
	old.reopen = func(context.Context) (accessibility.Backend, error) { return blocked, nil }
	blocked.reopen = func(context.Context) (accessibility.Backend, error) { return recovered, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).EventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	awaitOwnerSignal(t, old.eventReady, "initial event bind")

	bindCtx, cancelBind := context.WithCancel(ctx)
	firstReopen := make(chan error, 1)
	go func() { firstReopen <- session.accessibility.Reopen(bindCtx) }()
	awaitOwnerSignal(t, eventEntered, "blocked fresh event bind")
	cancelBind()
	if err := awaitOwnerSignal(t, firstReopen, "canceled reopen bind"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen canceled during bind = %v, want context.Canceled", err)
	}

	if err := session.accessibility.Reopen(ctx); err != nil {
		t.Fatalf("later explicit Reopen: %v", err)
	}
	recoveredStream := awaitOwnerSignal(t, recovered.eventReady, "event bind after retry")
	recoveredStream.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	select {
	case event, ok := <-stream:
		if !ok || event.Kind != "state-changed" {
			t.Fatalf("event after canceled bind = %+v, open=%v", event, ok)
		}
	case <-ctx.Done():
		t.Fatalf("wait stream did not recover after canceled bind: %v", ctx.Err())
	}
}

func TestAccessibilityCloseCancelsBlockedEventRetirement(t *testing.T) {
	old := newOwnerLifecycleBackend(50, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	fresh := newOwnerLifecycleBackend(51, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	old.retireEntered = make(chan struct{})
	old.retireGate = make(chan struct{})
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)

	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.accessibility.Reopen(context.Background()) }()
	awaitOwnerSignal(t, old.retireEntered, "event retirement during reopen")

	closeResult := make(chan error, 1)
	go func() { closeResult <- session.Close() }()
	awaitOwnerSignal(t, session.ctx.Done(), "session close cancellation")
	if err := awaitOwnerSignal(t, reopenResult, "reopen canceled by close"); !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Reopen during Close = %v, want cancellation", err)
	}
	if err := awaitOwnerSignal(t, closeResult, "Close after event retirement"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if old.closeCalls.Load() != 1 || fresh.closeCalls.Load() != 1 {
		t.Fatalf("backend close calls old=%d fresh=%d, want once each", old.closeCalls.Load(), fresh.closeCalls.Load())
	}
}

func TestAccessibilityOwnerPrunesClosedGenerations(t *testing.T) {
	const reopenCount = 12
	backends := make([]*ownerLifecycleBackend, reopenCount+1)
	for i := range backends {
		backends[i] = newOwnerLifecycleBackend(uint64(100+i), "applications", "snapshot", "find", "focused", "at-point", "reopen")
	}
	for i := 0; i < reopenCount; i++ {
		next := backends[i+1]
		if i+1 < len(backends) {
			backends[i].reopen = func(context.Context) (accessibility.Backend, error) { return next, nil }
		}
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backends[0])
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	for i := 0; i < reopenCount; i++ {
		if err := session.accessibility.Reopen(context.Background()); err != nil {
			t.Fatalf("Reopen %d: %v", i+1, err)
		}
		owner.mu.Lock()
		retained := len(owner.generations)
		owner.mu.Unlock()
		if retained != 1 {
			t.Fatalf("retained generations after reopen %d = %d, want 1", i+1, retained)
		}
		if backends[i].closeCalls.Load() != 1 {
			t.Fatalf("generation %d close calls = %d, want exactly once", i, backends[i].closeCalls.Load())
		}
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if backends[reopenCount].closeCalls.Load() != 1 {
		t.Fatalf("current generation close calls = %d, want exactly once", backends[reopenCount].closeCalls.Load())
	}
}

func TestAccessibilityOwnerRetainsRetiredCloseErrors(t *testing.T) {
	oldErr := errors.New("retired backend close failed")
	currentErr := errors.New("current backend close failed")
	old := newOwnerLifecycleBackend(130, "applications", "reopen")
	current := newOwnerLifecycleBackend(131, "applications", "reopen")
	old.closeErr = oldErr
	current.closeErr = currentErr
	old.reopen = func(context.Context) (accessibility.Backend, error) { return current, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	if err := session.accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if err := session.Close(); !errors.Is(err, oldErr) || !errors.Is(err, currentErr) {
		t.Fatalf("Close error = %v, want both retired and current backend failures", err)
	}
	if old.closeCalls.Load() != 1 || current.closeCalls.Load() != 1 {
		t.Fatalf("backend close calls old=%d current=%d, want once each", old.closeCalls.Load(), current.closeCalls.Load())
	}
}

func containsDiagnostic(diagnostics []string, fragment string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, fragment) {
			return true
		}
	}
	return false
}

func TestAccessibilityOwnerClosePreventsPendingReopenPublication(t *testing.T) {
	old := newOwnerLifecycleBackend(8, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	fresh := newOwnerLifecycleBackend(9, "applications", "snapshot", "find", "focused", "at-point", "reopen")
	entered := make(chan struct{})
	old.reopen = func(ctx context.Context) (accessibility.Backend, error) {
		close(entered)
		<-ctx.Done()
		return fresh, nil
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.accessibility.Reopen(context.Background()) }()
	awaitOwnerSignal(t, entered, "pending backend reopen")

	closeResult := make(chan error, 1)
	go func() { closeResult <- session.Close() }()
	awaitOwnerSignal(t, session.ctx.Done(), "session close linearization")
	if err := awaitOwnerSignal(t, reopenResult, "reopen rejected after session close"); !errors.Is(err, ErrSessionClosed) && !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("Reopen after Close = %v, want a closed-session error", err)
	}
	if err := awaitOwnerSignal(t, closeResult, "session close completion"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fresh.closed.Load() {
		t.Fatal("fresh backend from rejected reopen was not closed")
	}
}

var (
	ErrSessionClosed = capability.ErrSessionClosed
	ErrNilSession    = capability.ErrNilSession
	ErrUnavailable   = capability.ErrUnavailable
)

const CapabilityAccessibility = "accessibility"

type CapabilityStatus struct {
	Capability  string
	Available   bool
	Backend     string
	Diagnostics []string
	Failure     error
}
type sessionCore struct {
	ctx            context.Context
	cancel         context.CancelFunc
	lifecycleMu    sync.Mutex
	closed         bool
	capabilitiesMu sync.Mutex
	capabilities   map[string]CapabilityStatus
	owner          *Owner
	accessibility  testFacade
}
type testFacade struct{ owner *Owner }

func (f testFacade) Reopen(ctx context.Context) error { return f.owner.Reopen(ctx) }
func (f testFacade) Generation() uint64               { return f.owner.Generation() }
func (f testFacade) Applications(ctx context.Context) ([]accessibility.Application, error) {
	if !f.owner.HasBackend() {
		return nil, ErrUnavailable
	}
	return f.owner.Applications(ctx)
}
func newAccessibilityBackendOwner(s *sessionCore) *Owner {
	if s == nil {
		return New(nil)
	}
	return New(s)
}
func newSessionForTesting(_, _, _, _, _ any, backends ...accessibility.Backend) *sessionCore {
	s := &sessionCore{capabilities: make(map[string]CapabilityStatus)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.owner = New(s)
	s.accessibility = testFacade{s.owner}
	if len(backends) > 0 {
		s.owner.Install(backends[0])
		s.owner.refreshCapabilities()
	}
	return s
}
func (s *sessionCore) Context() context.Context { return s.ctx }
func (s *sessionCore) IsClosed() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closed || s.ctx == nil
}
func (s *sessionCore) LockPublication() (func(), error) {
	s.lifecycleMu.Lock()
	if s.closed || s.ctx == nil {
		s.lifecycleMu.Unlock()
		return nil, ErrSessionClosed
	}
	return s.lifecycleMu.Unlock, nil
}
func (s *sessionCore) NotifyWaiters() {}
func (s *sessionCore) MarkUnavailable(cause error) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	status := s.capabilities[CapabilityAccessibility]
	status.Available = false
	status.Failure = errors.Join(ErrUnavailable, cause)
	s.capabilities[CapabilityAccessibility] = status
}
func (s *sessionCore) DescribeBackend(backend accessibility.Backend) Status {
	result := Status{Backend: fmt.Sprintf("%T", backend), Operations: slices.Clone(backend.SupportedOperations())}
	if d, ok := backend.(interface{ Diagnostics() []string }); ok {
		result.Diagnostics = slices.Clone(d.Diagnostics())
	}
	return result
}
func (s *sessionCore) PublishStatus(value Status) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	status := s.capabilities[CapabilityAccessibility]
	status.Available = true
	status.Failure = nil
	status.Backend = value.Backend
	status.Diagnostics = value.Diagnostics
	s.capabilities[CapabilityAccessibility] = status
}
func (s *sessionCore) Capability(cap string) CapabilityStatus {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	return s.capabilities[cap]
}
func (s *sessionCore) Close() error {
	s.lifecycleMu.Lock()
	s.closed = true
	s.lifecycleMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return s.owner.Close()
}
