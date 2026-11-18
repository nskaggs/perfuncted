package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A partially assembled bundle must not invent a new ownership or deadline
// when no session exists to supply a backend policy.
func TestBackendContextWithoutOwnerRetainsCallerCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, bundle := range []*bundleBase{nil, {}} {
		child, finish := bundle.backendContext(parent, time.Millisecond)
		finish()
		if child != parent {
			t.Fatal("ownerless backend changed the caller's context")
		}
		cancel()
		if !errors.Is(child.Err(), context.Canceled) {
			t.Fatalf("caller cancellation was lost: %v", child.Err())
		}
	}
}

// An omitted operation timeout uses the session's configured medium policy
// and the returned cancellation remains owned by the backend operation.
func TestBackendContextZeroTimeoutUsesSessionPolicy(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	policy := session.Timeouts()
	before := time.Now()
	child, finish := session.Screen.backendContext(context.Background(), 0)
	deadline, ok := child.Deadline()
	if !ok || deadline.Before(before) || deadline.After(before.Add(policy.Medium+time.Second)) {
		finish()
		t.Fatalf("backend deadline %v did not follow medium policy %s", deadline, policy.Medium)
	}
	finish()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("backend cancellation did not end the operation: %v", child.Err())
	}
}
