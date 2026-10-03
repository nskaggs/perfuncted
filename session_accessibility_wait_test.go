package perfuncted

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type waitAccessibilityBackend struct {
	calls               int
	incompleteSnapshots int
	alwaysIncomplete    bool
}

func (b *waitAccessibilityBackend) SupportedOperations() []string { return []string{"find", "focused"} }
func (b *waitAccessibilityBackend) Applications(context.Context) ([]accessibility.Application, error) {
	return nil, nil
}
func (b *waitAccessibilityBackend) Snapshot(_ context.Context, root accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	generation := root.Generation
	if generation == 0 {
		generation = 1
	}
	root.Generation = generation
	rootNode := accessibility.Node{ID: root, Role: "application"}
	nodes := []accessibility.Node{rootNode}
	if b.calls >= 2 {
		nodes = append(nodes, accessibility.Node{
			ID:         accessibility.NodeID{BusName: root.BusName, ObjectPath: "/save", Generation: generation},
			Parent:     root,
			Name:       "Save",
			Text:       "Saved",
			Role:       "button",
			States:     []string{"enabled", "focused"},
			Attributes: map[string]string{"kind": "primary"},
		})
	}
	snapshot := accessibility.Snapshot{Root: rootNode, Nodes: nodes, Generation: generation}
	if b.alwaysIncomplete || b.calls <= b.incompleteSnapshots {
		snapshot.Truncated = true
		snapshot.TruncationReasons = []string{"test completeness boundary"}
	}
	return snapshot, nil
}
func (b *waitAccessibilityBackend) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	snapshot, err := b.Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}
func (b *waitAccessibilityBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Focused: true}, nil
}
func (b *waitAccessibilityBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{}, accessibility.ErrNotFound
}
func (b *waitAccessibilityBackend) Close() error { return nil }

func TestAccessibilityWaitConditionsRefreshUntilSatisfied(t *testing.T) {
	backend := &waitAccessibilityBackend{}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"}
	if err := session.Wait(ctx, AccessibilityNodeExists(root, accessibility.Query{Role: "button"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityNodeExists: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityStateContains(root, accessibility.Query{Name: "Save"}, []string{"focused", "enabled"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityStateContains: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityTextContains(root, "Saved", accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityTextContains: %v", err)
	}
	if backend.calls < 3 {
		t.Fatalf("backend calls = %d, want repeated authoritative refreshes", backend.calls)
	}
	_ = session.Close()
}

func TestAccessibilityNodeExistsWaitRetriesIncompleteSnapshots(t *testing.T) {
	backend := &waitAccessibilityBackend{incompleteSnapshots: 1}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	condition := AccessibilityNodeExists(
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"},
		accessibility.Query{Role: "button"},
		accessibility.SnapshotOptions{},
	)
	var failures waitEvaluateFailures
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || err != nil || failures.consecutive != 1 || failures.total != 1 {
		t.Fatalf("first incomplete evaluation = ok %t, err %v, consecutive %d, total %d; want retryable false", ok, err, failures.consecutive, failures.total)
	}
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); !ok || err != nil || failures.consecutive != 0 || failures.total != 1 {
		t.Fatalf("complete evaluation = ok %t, err %v, consecutive %d, total %d; want success", ok, err, failures.consecutive, failures.total)
	}
	if backend.calls != 2 {
		t.Fatalf("snapshot calls = %d, want incomplete then complete", backend.calls)
	}
}

func TestAccessibilityNodeExistsWaitSurfacesSustainedIncompleteSnapshots(t *testing.T) {
	backend := &waitAccessibilityBackend{alwaysIncomplete: true}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	condition := AccessibilityNodeExists(
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"},
		accessibility.Query{Role: "button"},
		accessibility.SnapshotOptions{},
	)
	var failures waitEvaluateFailures
	for i := 1; i < waitEvaluateFailureLimit; i++ {
		if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || err != nil {
			t.Fatalf("incomplete evaluation %d = ok %t, err %v; want retryable false", i, ok, err)
		}
	}
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || !errors.Is(err, accessibility.ErrIncompleteSnapshot) {
		t.Fatalf("terminal incomplete evaluation = ok %t, err %v; want ErrIncompleteSnapshot", ok, err)
	}
	if backend.calls != waitEvaluateFailureLimit {
		t.Fatalf("snapshot calls = %d, want bounded failure limit %d", backend.calls, waitEvaluateFailureLimit)
	}
}

type waitAccessibilityEventBackend struct {
	waitAccessibilityBackend
	events chan accessibility.Event

	// opened closes once the wait machinery has opened an event stream, and
	// delivered closes once an event has been handed to that stream. A test
	// needs both to claim an event arrived during a wait, rather than before
	// the wait was subscribed.
	opened        chan struct{}
	delivered     chan struct{}
	openedOnce    sync.Once
	deliveredOnce sync.Once
}

func newWaitAccessibilityEventBackend() *waitAccessibilityEventBackend {
	return &waitAccessibilityEventBackend{
		events:    make(chan accessibility.Event, 1),
		opened:    make(chan struct{}),
		delivered: make(chan struct{}),
	}
}

func (b *waitAccessibilityEventBackend) Events(
	ctx context.Context,
	_ accessibility.EventOptions,
) (<-chan accessibility.Event, error) {
	b.openedOnce.Do(func() { close(b.opened) })
	out := make(chan accessibility.Event, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-b.events:
				if !ok {
					return
				}
				select {
				case out <- event:
					b.deliveredOnce.Do(func() { close(b.delivered) })
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func TestWaitChangesIncludesAccessibilityEvents(t *testing.T) {
	backend := newWaitAccessibilityEventBackend()
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	wake := session.waitChanges()
	backend.events <- accessibility.Event{Kind: "focus"}
	select {
	case <-wake.done:
	case <-time.After(time.Second):
		t.Fatal("accessibility event did not wake waiter")
	}
}

func TestWaitEpochKeepsWakeAttributionAcrossRapidNotifications(t *testing.T) {
	hub := newInvalidationHub()
	epoch := hub.subscribe()
	observed := make(chan waitChange, 1)
	go func() {
		<-epoch.done
		observed <- epoch.change
	}()
	hub.notify(waitChange{source: "window"})
	hub.notify(waitChange{source: "accessibility", event: &accessibility.Event{Kind: "state-changed", Property: "enabled"}})
	select {
	case change := <-observed:
		if change.source != "window" || change.event != nil {
			t.Fatalf("first epoch attribution = %+v, want window without event", change)
		}
	case <-time.After(time.Second):
		t.Fatal("epoch waiter did not wake")
	}
}

func TestWaitWithEvidenceReportsAccessibilityWake(t *testing.T) {
	backend := newWaitAccessibilityEventBackend()
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()

	// The wait opens its event stream lazily, on its first evaluation, so the
	// event is sent once the stream exists. The condition then blocks until the
	// event has entered the stream and only reports satisfied on the evaluation
	// after the wake it produced, so the wait ends on the wakeup rather than on
	// a timeout. The poll interval is long enough that the event, not the poll,
	// is what wakes the wait, which is what the evidence asserts.
	go func() {
		<-backend.opened
		backend.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	evaluations := 0
	evidence, err := session.WaitWithEvidence(ctx, sessionCondition("after event", func(ctx context.Context, _ *Session) (bool, error) {
		evaluations++
		if evaluations == 1 {
			select {
			case <-backend.delivered:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			return false, nil
		}
		return true, nil
	}), WaitEvery(250*time.Millisecond))
	if err != nil {
		t.Fatalf("WaitWithEvidence: %v", err)
	}
	if evidence.AccessibilityWakeups == 0 || evidence.LastAccessibilityEvent == nil {
		t.Fatalf("wait evidence = %+v, want accessibility wakeup and event", evidence)
	}
	if evidence.LastAccessibilityEvent.Kind != "state-changed" {
		t.Fatalf("last accessibility event = %+v", evidence.LastAccessibilityEvent)
	}
	if evidence.PollWakeups != 0 {
		t.Fatalf("wait evidence = %+v, want the wake attributed to the event rather than a poll", evidence)
	}
}
