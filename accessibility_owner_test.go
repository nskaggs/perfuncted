package perfuncted

import (
	"context"
	"errors"
	"strings"
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
	return ctx.Err()
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

func accessibilityOwnerForTest(t *testing.T, session *Session) *accessibilityBackendOwner {
	t.Helper()
	owner, ok := session.Accessibility.backend.(*accessibilityBackendOwner)
	if !ok {
		t.Fatalf("accessibility backend = %T, want *accessibilityBackendOwner", session.Accessibility.backend)
	}
	return owner
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
	if session.Accessibility.Generation() <= old.Generation() {
		t.Fatalf("generation after reopen = %d, old generation = %d", session.Accessibility.Generation(), old.Generation())
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	oldStream := awaitOwnerSignal(t, old.eventReady, "initial event bind")
	if oldStream.events == nil {
		t.Fatal("initial event bind returned no stream")
	}

	if err := session.Accessibility.Reopen(ctx); err != nil {
		t.Fatalf("Reopen with optional event failure: %v", err)
	}
	failedAttempt := awaitOwnerSignal(t, failed.eventReady, "failed event bind result")
	assertOwnerEventFailure(t, failedAttempt, failed, wantError)
	assertAccessibilityEventLimit(t, session, true)

	if err := session.Accessibility.Reopen(ctx); err != nil {
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

func assertAccessibilityEventLimit(t *testing.T, session *Session, wantLimit bool) {
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	oldStream := awaitOwnerSignal(t, old.eventReady, "initial event bind")
	oldStream.close()

	if err := session.Accessibility.Reopen(ctx); err != nil {
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := accessibilityOwnerForTest(t, session).eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("eventsAcrossReopens: %v", err)
	}
	awaitOwnerSignal(t, old.eventReady, "initial event bind")

	bindCtx, cancelBind := context.WithCancel(ctx)
	firstReopen := make(chan error, 1)
	go func() { firstReopen <- session.Accessibility.Reopen(bindCtx) }()
	awaitOwnerSignal(t, eventEntered, "blocked fresh event bind")
	cancelBind()
	if err := awaitOwnerSignal(t, firstReopen, "canceled reopen bind"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reopen canceled during bind = %v, want context.Canceled", err)
	}

	if err := session.Accessibility.Reopen(ctx); err != nil {
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)

	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.Accessibility.Reopen(context.Background()) }()
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backends[0])
	t.Cleanup(func() { _ = session.Close() })
	owner := accessibilityOwnerForTest(t, session)

	for i := 0; i < reopenCount; i++ {
		if err := session.Accessibility.Reopen(context.Background()); err != nil {
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	if err := session.Accessibility.Reopen(context.Background()); err != nil {
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
