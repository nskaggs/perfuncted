package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type closingActionReceiptBackend struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *closingActionReceiptBackend) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{Index: 3, Name: "private-action"}, nil
}
func (b *closingActionReceiptBackend) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{Index: 5, Name: "private-default"}, nil
}

// A provider may return the selected action after the session has closed;
// its acknowledged result must not leak action metadata or report success.
func TestAccessibilityActionReceiptRejectsClosedSession(t *testing.T) {
	for _, name := range []string{"by-name", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingActionReceiptBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var action accessibility.Action
			var err error
			id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/button", Generation: 1}
			if name == "by-name" {
				action, err = session.Accessibility.InvokeActionByName(context.Background(), id, "press")
			} else {
				action, err = session.Accessibility.InvokeDefaultAction(context.Background(), id)
			}
			if !errors.Is(err, ErrSessionClosed) || action.Name != "" || backend.calls != 1 {
				t.Fatalf("%s receipt after session closure: action=%+v err=%v calls=%d", name, action, err, backend.calls)
			}
		})
	}
}
