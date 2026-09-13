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

// TestAdversarialBundleWrappersCoverAutomationSurface exercises every typed
// bundle wrapper once. A typo in a wrapper's gated operation name would
// otherwise surface only as ErrUnsupported on a live bus.
func TestAdversarialBundleWrappersCoverAutomationSurface(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 5}, accessibilityAutomationSpy: spy}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 5}
	bundle := session.Accessibility
	calls := []struct {
		name string
		call func() error
	}{
		{"action", func() error { return bundle.InvokeAction(ctx, id, 0) }},
		{"action-name", func() error { _, err := bundle.InvokeActionByName(ctx, id, "press"); return err }},
		{"default-action", func() error { _, err := bundle.InvokeDefaultAction(ctx, id); return err }},
		{"focus", func() error { return bundle.FocusNode(ctx, id) }},
		{"scroll", func() error { return bundle.ScrollTo(ctx, id, accessibility.ScrollAnyWhere) }},
		{"scroll-point", func() error { return bundle.ScrollToPoint(ctx, id, accessibility.CoordTypeScreen, 1, 2) }},
		{"set-position", func() error { return bundle.SetPosition(ctx, id, 1, 2, accessibility.CoordTypeScreen) }},
		{"set-size", func() error { return bundle.SetSize(ctx, id, 3, 4) }},
		{"set-extents", func() error { return bundle.SetExtents(ctx, id, 1, 2, 3, 4, accessibility.CoordTypeScreen) }},
		{"set-value", func() error { return bundle.SetValue(ctx, id, 0.5) }},
		{"text", func() error { return bundle.ReplaceEditableText(ctx, id, "x") }},
		{"replace", func() error { return bundle.ReplaceText(ctx, id, 0, 1, "x") }},
		{"insert", func() error { return bundle.InsertText(ctx, id, 0, "x") }},
		{"delete", func() error { return bundle.DeleteText(ctx, id, 0, 1) }},
		{"copy", func() error { return bundle.CopyText(ctx, id, 0, 1) }},
		{"cut", func() error { return bundle.CutText(ctx, id, 0, 1) }},
		{"paste", func() error { return bundle.PasteText(ctx, id, 0) }},
		{"caret", func() error { return bundle.SetCaretOffset(ctx, id, 1) }},
		{"selection", func() error { return bundle.SetTextSelection(ctx, id, 0, 0, 1) }},
		{"add-selection", func() error { return bundle.AddTextSelection(ctx, id, 0, 1) }},
		{"remove-selection", func() error { return bundle.RemoveTextSelection(ctx, id, 0) }},
		{"document-selections", func() error { return bundle.SetTextSelections(ctx, id, nil) }},
		{"select-child", func() error { return bundle.SelectChild(ctx, id, 0) }},
		{"deselect-child", func() error { return bundle.DeselectChild(ctx, id, 0) }},
		{"select-all", func() error { return bundle.SelectAll(ctx, id) }},
		{"clear-selection", func() error { return bundle.ClearSelection(ctx, id) }},
		{"deselect-all", func() error { return bundle.DeselectAll(ctx, id) }},
		{"deselect-selected-child", func() error { return bundle.DeselectSelectedChild(ctx, id) }},
		{"select-row", func() error { return bundle.SelectRow(ctx, id, 0) }},
		{"deselect-row", func() error { return bundle.DeselectRow(ctx, id, 0) }},
		{"select-column", func() error { return bundle.SelectColumn(ctx, id, 0) }},
		{"deselect-column", func() error { return bundle.DeselectColumn(ctx, id, 0) }},
		{"scroll-view", func() error { return bundle.ScrollNodeIntoView(ctx, id) }},
	}
	for _, tc := range calls {
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(spy.calls) != len(calls) {
		t.Fatalf("automation calls = %d, want %d (%v)", len(spy.calls), len(calls), spy.calls)
	}
}
