package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type closingSemanticErrorActionBackend struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *closingSemanticErrorActionBackend) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{}, &accessibility.ActionInvocationError{
		Action:   accessibility.Action{Name: "private-action", Index: 7},
		Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleGeneration,
	}
}
func (b *closingSemanticErrorActionBackend) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{}, &accessibility.ActionInvocationError{
		Action:   accessibility.Action{Name: "private-default", Index: 0},
		Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleGeneration,
	}
}

// A provider's dispatch error may embed sensitive node/action metadata. Once
// the session closes, even this failure receipt is no longer authorized.
func TestAccessibilitySemanticErrorReceiptSuppressesPrivateMetadataAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"named", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingSemanticErrorActionBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.private", ObjectPath: "/secret", Generation: 1}, Name: "private-button"}
			actionName := "press"
			if name == "default" {
				actionName = ""
			}
			receipt, err := session.Accessibility.invokeSemanticActionOnNode(context.Background(), node, actionName)
			if !errors.Is(err, ErrSessionClosed) || backend.calls != 1 || receipt.Node.Name != "" || receipt.Action.Name != "" {
				t.Fatalf("%s leaked semantic receipt after closure: receipt=%+v err=%v calls=%d", name, receipt, err, backend.calls)
			}
		})
	}
}
