package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

// A stable window handle is not evidence of its disappearance after the
// observer's own Session has closed. In particular, the capability admission
// error must not be mistaken for an unavailable compositor and retried.
func TestWindowClosedConditionPreservesClosedSessionError(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-handle", Title: "private"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	handle := &Window{id: WindowID{session: session, native: "private-handle"}}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	matched, err := WindowClosed(handle).evaluate(context.Background(), session)
	if matched || !errors.Is(err, ErrSessionClosed) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed owner incorrectly proved disappearance: matched=%v err=%v", matched, err)
	}
}
