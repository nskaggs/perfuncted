package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func semanticActionCoverageFixture(spy *accessibilityAutomationSpy) (*Session, accessibility.NodeID) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 19}
	target := accessibility.NodeID{BusName: root.BusName, ObjectPath: "/app/save", Generation: root.Generation}
	app := accessibility.Node{ID: root, Role: "application", Name: "Editor"}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: root.Generation},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{
			Root: app,
			Nodes: []accessibility.Node{
				app,
				{ID: target, Parent: root, Role: "button", Name: "Save", Actions: []accessibility.Action{{Index: 7, Name: "press"}}},
			},
			Generation: root.Generation,
		},
	}
	return NewSessionForTesting(nil, nil, nil, nil, nil, fake), root
}

func TestAccessibilitySemanticActionDoesNotInventReceiptForProviderError(t *testing.T) {
	providerErr := errors.New("provider rejected action before dispatch")
	spy := &accessibilityAutomationSpy{
		exactAction: accessibility.Action{Index: 7, Name: "press"},
		invokeErr:   providerErr,
	}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSemanticAction(
		context.Background(), root, accessibility.Query{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if !errors.Is(err, providerErr) {
		t.Fatalf("InvokeSemanticAction error = %v, want provider error", err)
	}
	if receipt.Node.ID != (accessibility.NodeID{}) || receipt.Action != (accessibility.Action{}) || receipt.Dispatch != "" {
		t.Fatalf("unclassified provider failure produced a dispatch receipt: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

func TestAccessibilitySemanticActionSuppressesReceiptWhenCallerCancelsDuringFailure(t *testing.T) {
	providerErr := errors.New("provider returned private action metadata")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 7, Name: "private-action"}}
	spy.dispatchCheck = func() error {
		cancel()
		return &accessibility.ActionInvocationError{
			Action: spy.exactAction, Dispatch: accessibility.DispatchNotSent, Err: providerErr,
		}
	}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSemanticAction(
		ctx, root, accessibility.Query{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if !errors.Is(err, context.Canceled) || errors.Is(err, providerErr) {
		t.Fatalf("InvokeSemanticAction error = %v, want only caller cancellation", err)
	}
	if receipt.Node.ID != (accessibility.NodeID{}) || receipt.Action != (accessibility.Action{}) || receipt.Dispatch != "" {
		t.Fatalf("canceled action exposed provider metadata: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

func TestAccessibilityEventsRejectNilContextBeforeProviderDispatch(t *testing.T) {
	provider := &bundleAccessibilityFake{gen: 1}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	defer session.Close()

	stream, err := session.Accessibility.Events(nil, accessibility.EventOptions{})
	if stream != nil || err == nil {
		t.Fatalf("nil-context event request returned stream=%v err=%v", stream, err)
	}
	if provider.eventCalls != 0 {
		t.Fatalf("provider received %d event requests with nil caller context", provider.eventCalls)
	}
}
