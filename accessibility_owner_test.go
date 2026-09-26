package perfuncted

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type ownerLifecycleBackend struct {
	operations []string
	generation atomic.Uint64
	closed     atomic.Bool
	usedClosed atomic.Bool
	closeOnce  sync.Once
	closeDone  chan struct{}

	appsEntered chan struct{}
	appsGate    <-chan struct{}
	appsOnce    sync.Once
	apps        []accessibility.Application

	eventReady chan ownerEventFixture
	reopen     func(context.Context) (accessibility.Backend, error)
}

type ownerEventFixture struct {
	events chan accessibility.Event
	close  func()
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
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.closeDone)
	})
	return nil
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
	stream := make(chan accessibility.Event, 4)
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(func() { close(stream) }) }
	b.eventReady <- ownerEventFixture{events: stream, close: closeStream}
	go func() {
		<-ctx.Done()
		closeStream()
	}()
	return stream, nil
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() {
		releaseOldCall()
		_ = session.Close()
	})

	oldCall := make(chan error, 1)
	go func() {
		_, err := session.Accessibility.Applications(context.Background())
		oldCall <- err
	}()
	awaitOwnerSignal(t, old.appsEntered, "old accessibility call")

	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	apps, err := session.Accessibility.Applications(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "fresh" {
		t.Fatalf("Applications after reopen = (%+v, %v), want fresh backend", apps, err)
	}
	if old.closed.Load() {
		t.Fatal("retired backend closed while an accessibility call still held its lease")
	}

	releaseOldCall()
	if err := awaitOwnerSignal(t, oldCall, "in-flight accessibility call"); err != nil {
		t.Fatalf("in-flight Applications: %v", err)
	}
	awaitOwnerSignal(t, old.closeDone, "retired backend close")
	if old.usedClosed.Load() {
		t.Fatal("accessibility call used the backend after Close")
	}
	if session.Accessibility.Generation() <= old.Generation() {
		t.Fatalf("generation after reopen = %d, old generation = %d", session.Accessibility.Generation(), old.Generation())
	}
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.Accessibility.Reopen(context.Background()) }()
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

func TestAccessibilityWaitRebindsToReopenedBackendBeforeWaking(t *testing.T) {
	old := newOwnerLifecycleBackend(2, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(3, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	evaluations := make(chan int32, 8)
	var count atomic.Int32
	var eventObserved atomic.Bool
	waitResult := make(chan struct {
		evidence WaitEvidence
		err      error
	}, 1)
	go func() {
		evidence, err := session.WaitWithEvidence(ctx, sessionCondition("reopened event", func(context.Context, *Session) (bool, error) {
			current := count.Add(1)
			evaluations <- current
			return eventObserved.Load(), nil
		}), WaitEvery(time.Hour))
		waitResult <- struct {
			evidence WaitEvidence
			err      error
		}{evidence: evidence, err: err}
	}()
	oldEvents := awaitOwnerSignal(t, old.eventReady, "initial accessibility event subscription")
	oldEvents.close()

	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.Accessibility.Reopen(ctx) }()
	newEvents := awaitOwnerSignal(t, fresh.eventReady, "reopened accessibility event subscription")
	if err := awaitOwnerSignal(t, reopenResult, "reopen and event rebinding"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	for current := int32(0); current < 2; {
		current = awaitOwnerSignal(t, evaluations, "wait evaluation after reopen")
	}

	eventObserved.Store(true)
	newEvents.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	result := awaitOwnerSignal(t, waitResult, "accessibility event wake")
	if result.err != nil {
		t.Fatalf("WaitWithEvidence: %v", result.err)
	}
	if result.evidence.PollWakeups != 0 {
		t.Fatalf("poll wakeups = %d, want event-only wake", result.evidence.PollWakeups)
	}
	if result.evidence.AccessibilityWakeups == 0 || result.evidence.LastAccessibilityEvent == nil || result.evidence.LastAccessibilityEvent.Kind != "state-changed" {
		t.Fatalf("wait evidence = %+v, want event from reopened backend", result.evidence)
	}
}
