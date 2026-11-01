package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, b)
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil, b)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := owner.eventsAcrossReopens(ctx, accessibility.EventOptions{})
	if !errors.Is(err, context.Canceled) || stream != nil {
		t.Fatalf("canceled persistent registration returned stream=%v error=%v", stream, err)
	}
	if b.eventCalls.Load() != 0 {
		t.Fatalf("canceled registration subscribed backend: %d", b.eventCalls.Load())
	}
}
