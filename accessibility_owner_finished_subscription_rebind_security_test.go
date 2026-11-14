package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
