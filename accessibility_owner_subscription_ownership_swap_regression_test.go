package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
	owner.install(backend)
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
