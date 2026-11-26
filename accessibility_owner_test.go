package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coreOnlyAccessibilityBackend struct{ accessibility.Backend }
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

func TestAccessibilityWaitRebindsToReopenedBackendBeforeWaking(t *testing.T) {
	old := newOwnerLifecycleBackend(2, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(3, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
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
		evidence, err := session.WaitWithEvidence(ctx, sessionCondition("reopened event", func(context.Context, *sessionCore) (bool, error) {
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
	// The subscription can publish before WaitWithEvidence performs its first
	// condition evaluation. Synchronize on that evaluation so the close/reopen
	// sequence cannot race ahead of the wait and strand the test for an hour.
	if got := awaitOwnerSignal(t, evaluations, "initial wait evaluation"); got != 1 {
		t.Fatalf("initial wait evaluation = %d, want 1", got)
	}
	oldEvents.close()

	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.accessibility.Reopen(ctx) }()
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
