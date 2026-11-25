package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"reflect"
	"strings"
	"testing"
	"time"
)

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
	owner.install(backend)
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, original)
	owner := accessibilityOwnerForTest(t, session)
	t.Cleanup(func() { _ = session.Close() })
	for attempt := 0; attempt < 2; attempt++ {
		if err := session.Accessibility.Reopen(context.Background()); !errors.Is(err, denied) {
			t.Fatalf("reopen %d hid provider readiness error: %v", attempt, err)
		}
		if !owner.hasBackend() || original.closeCalls.Load() != 0 {
			t.Fatalf("reopen %d retired usable original backend: available=%t closes=%d", attempt, owner.hasBackend(), original.closeCalls.Load())
		}
		if applications, err := session.Accessibility.Applications(context.Background()); err != nil || len(applications) != 0 {
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

func TestAccessibilityOwnerAdvancesReopenedBackendThatReusesGeneration(t *testing.T) {
	old := newOwnerLifecycleBackend(44, "applications", "reopen")
	fresh := newOwnerLifecycleBackend(44, "applications", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("reopen backend with reused generation: %v", err)
	}
	if got, want := fresh.Generation(), uint64(45); got != want {
		t.Fatalf("fresh backend generation = %d, want invalidated generation %d", got, want)
	}
	if got := owner.Generation(); got != fresh.Generation() || !owner.hasBackend() {
		t.Fatalf("owner published generation %d with backend available=%t; fresh generation=%d", got, owner.hasBackend(), fresh.Generation())
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
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

	err := session.Accessibility.Reopen(context.Background())
	if !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("reopen after owner closure = %v, want disconnected", err)
	}
	if closeErr := awaitOwnerSignal(t, closeDone, "owner close after reopen lease release"); closeErr != nil {
		t.Fatalf("owner.Close: %v", closeErr)
	}
	if owner.hasBackend() {
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
	session := &Session{ctx: ctx}
	owner := newAccessibilityBackendOwner(session)
	owner.install(backend)
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
