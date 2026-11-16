package perfuncted

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner := accessibilityOwnerForTest(t, session)
	stream, err := owner.eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 4})
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

	if err := session.Accessibility.Reopen(ctx); err != nil {
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
