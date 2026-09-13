package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

func TestAdversarialResolveManagedWindowIsSingleAuthority(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{
		{NativeID: "one", Title: "Firefox", PID: 11},
		{NativeID: "two", Title: "Firefox", PID: 22},
	}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 11}}, gen: 1}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	if _, err := session.Accessibility.WindowRoot(context.Background(), "one"); err != nil {
		t.Fatalf("WindowRoot one: %v", err)
	}
	if _, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowTitle: "Firefox"}); !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("shared title via FindApplication = %v, want ambiguous from the same resolver", err)
	}
}

func TestAdversarialSetTextSelectionRequiresExplicitSelection(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 3}, accessibilityAutomationSpy: spy}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/entry", Generation: 3}
	if err := session.Accessibility.SetTextSelection(context.Background(), id, 2, 4, 9); err != nil {
		t.Fatalf("SetTextSelection: %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "selection" {
		t.Fatalf("calls = %v, want one explicit selection call", spy.calls)
	}
}
