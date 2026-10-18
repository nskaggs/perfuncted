package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackendContextReusesEarlierCallerDeadline(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	child, finish := session.Screen.backendContext(parent, 2*time.Minute)
	defer finish()
	if child != parent {
		t.Fatalf("backend context duplicated the earlier caller deadline; want exact parent context")
	}
	if err := child.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBackendContextAddsShorterBackendTimeout(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	child, finish := session.Screen.backendContext(parent, 10*time.Millisecond)
	defer finish()
	if child == parent {
		t.Fatal("backend deadline is shorter; child required")
	}
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Before(parentDeadline) {
		t.Fatalf("backend deadline %v should precede caller %v", childDeadline, parentDeadline)
	}
	<-child.Done()
	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatalf("child expiry=%v", child.Err())
	}
	if parent.Err() != nil {
		t.Fatalf("backend expiration canceled caller: %v", parent.Err())
	}
}

func TestBackendContextUnboundedCallerStaysBounded(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	child, finish := session.Screen.backendContext(context.Background(), time.Second)
	defer finish()
	deadline, ok := child.Deadline()
	if !ok || time.Until(deadline) > time.Second {
		t.Fatalf("missing or excessive backend deadline: %v, %v", deadline, ok)
	}
}
