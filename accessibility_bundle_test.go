package perfuncted

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

type bundleAccessibilityFake struct {
	apps []accessibility.Application
	gen  uint64
}

type accessibilityAutomationSpy struct{ calls []string }

func (s *accessibilityAutomationSpy) mark(name string) { s.calls = append(s.calls, name) }
func (s *accessibilityAutomationSpy) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	s.mark("action")
	return nil
}
func (s *accessibilityAutomationSpy) InvokeActionByName(context.Context, accessibility.NodeID, string) error {
	s.mark("action-name")
	return nil
}
func (s *accessibilityAutomationSpy) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	s.mark("default-action")
	return accessibility.Action{Index: 0, Name: "default"}, nil
}
func (s *accessibilityAutomationSpy) GrabFocus(context.Context, accessibility.NodeID) error {
	s.mark("focus")
	return nil
}
func (s *accessibilityAutomationSpy) ScrollTo(context.Context, accessibility.NodeID, accessibility.ScrollType) error {
	s.mark("scroll")
	return nil
}
func (s *accessibilityAutomationSpy) ScrollToPoint(context.Context, accessibility.NodeID, accessibility.ScrollType, int, int) error {
	s.mark("scroll-point")
	return nil
}
func (s *accessibilityAutomationSpy) SetCurrentValue(context.Context, accessibility.NodeID, float64) error {
	s.mark("value")
	return nil
}
func (s *accessibilityAutomationSpy) SetValue(context.Context, accessibility.NodeID, float64) error {
	s.mark("set-value")
	return nil
}
func (s *accessibilityAutomationSpy) SetTextContents(context.Context, accessibility.NodeID, string) error {
	s.mark("text")
	return nil
}
func (s *accessibilityAutomationSpy) ReplaceText(context.Context, accessibility.NodeID, int32, int32, string) error {
	s.mark("replace")
	return nil
}
func (s *accessibilityAutomationSpy) InsertText(context.Context, accessibility.NodeID, int32, string) error {
	s.mark("insert")
	return nil
}
func (s *accessibilityAutomationSpy) DeleteText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("delete")
	return nil
}
func (s *accessibilityAutomationSpy) CopyText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("copy")
	return nil
}
func (s *accessibilityAutomationSpy) CutText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("cut")
	return nil
}
func (s *accessibilityAutomationSpy) PasteText(context.Context, accessibility.NodeID, int32) error {
	s.mark("paste")
	return nil
}
func (s *accessibilityAutomationSpy) SetCaretOffset(context.Context, accessibility.NodeID, int32) error {
	s.mark("caret")
	return nil
}
func (s *accessibilityAutomationSpy) SetTextSelection(context.Context, accessibility.NodeID, int32, int32, int32) error {
	s.mark("selection")
	return nil
}
func (s *accessibilityAutomationSpy) AddTextSelection(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("add-selection")
	return nil
}
func (s *accessibilityAutomationSpy) RemoveTextSelection(context.Context, accessibility.NodeID, int32) error {
	s.mark("remove-selection")
	return nil
}
func (s *accessibilityAutomationSpy) SelectChild(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-child")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectChild(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-child")
	return nil
}
func (s *accessibilityAutomationSpy) SelectAll(context.Context, accessibility.NodeID) error {
	s.mark("select-all")
	return nil
}
func (s *accessibilityAutomationSpy) ClearSelection(context.Context, accessibility.NodeID) error {
	s.mark("clear-selection")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectAll(context.Context, accessibility.NodeID) error {
	s.mark("deselect-all")
	return nil
}
func (s *accessibilityAutomationSpy) SelectRow(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-row")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectRow(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-row")
	return nil
}
func (s *accessibilityAutomationSpy) SelectColumn(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-column")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectColumn(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-column")
	return nil
}

type accessibilityAutomationFake struct {
	*bundleAccessibilityFake
	*accessibilityAutomationSpy
}

type accessibilityReopenerFake struct {
	*bundleAccessibilityFake
	fresh accessibility.Backend
	closed bool
}

func (f *accessibilityReopenerFake) SupportedOperations() []string {
	return append(f.bundleAccessibilityFake.SupportedOperations(), "reopen")
}
func (f *accessibilityReopenerFake) Close() error { f.closed = true; return nil }
func (f *accessibilityReopenerFake) Reopen(context.Context) (accessibility.Backend, error) { return f.fresh, nil }

func (*accessibilityAutomationFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "scroll-to-point", "set-current-value", "set-value", "set-text-contents", "replace-text", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-all", "select-row", "deselect-row", "select-column", "deselect-column", "window-root", "reopen"}
}

func (f *bundleAccessibilityFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events"}
}
func (f *bundleAccessibilityFake) Applications(context.Context) ([]accessibility.Application, error) {
	return append([]accessibility.Application(nil), f.apps...), nil
}
func (f *bundleAccessibilityFake) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	return accessibility.Snapshot{Generation: f.gen, Source: "fake"}, nil
}
func (f *bundleAccessibilityFake) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return []accessibility.Node{{Name: "ok"}}, nil
}
func (f *bundleAccessibilityFake) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Name: "focused"}, nil
}
func (f *bundleAccessibilityFake) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{Name: "point"}, nil
}
func (f *bundleAccessibilityFake) Close() error { return nil }
func (f *bundleAccessibilityFake) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return f.apps[0], nil
}
func (f *bundleAccessibilityFake) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	if target.ID == "missing" {
		return accessibility.WindowScope{}, accessibility.ErrNotFound
	}
	return accessibility.WindowScope{WindowID: target.ID, Root: accessibility.NodeID{BusName: "org.test", ObjectPath: "/window", Generation: f.gen}}, nil
}
func (f *bundleAccessibilityFake) Generation() uint64              { return f.gen }
func (f *bundleAccessibilityFake) Invalidate(accessibility.NodeID) { f.gen++ }

func TestAccessibilityBundleDelegatesOptionalSurface(t *testing.T) {
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}}}, gen: 7}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{Name: "fire"})
	if err != nil || app.Name != "Firefox" {
		t.Fatalf("FindApplication = %+v, %v", app, err)
	}
	if got := session.Accessibility.Generation(); got != 7 {
		t.Fatalf("generation = %d", got)
	}
	session.Accessibility.Invalidate(accessibility.NodeID{})
	if got := session.Accessibility.Generation(); got != 8 {
		t.Fatalf("generation after invalidation = %d", got)
	}
}

func TestAccessibilityBundleDelegatesTypedAutomation(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 3}, accessibilityAutomationSpy: spy}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 3}
	if err := session.Accessibility.FocusNode(context.Background(), id); err != nil {
		t.Fatalf("FocusNode: %v", err)
	}
	if err := session.Accessibility.SetCurrentValue(context.Background(), id, 0.5); err != nil {
		t.Fatalf("SetCurrentValue: %v", err)
	}
	if _, err := session.Accessibility.InvokeDefaultAction(context.Background(), id); err != nil {
		t.Fatalf("InvokeDefaultAction: %v", err)
	}
	if len(spy.calls) != 3 || spy.calls[0] != "focus" || spy.calls[1] != "value" || spy.calls[2] != "default-action" {
		t.Fatalf("automation calls = %v", spy.calls)
	}
}

func TestAccessibilityBundleExplicitReopenSwapsBackend(t *testing.T) {
	old := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "old"}}}, gen: 1}}
	fresh := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "fresh"}}}, gen: 2}
	old.fresh = fresh
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	defer session.Close()
	if err := session.Accessibility.ReopenAccessibility(context.Background()); err != nil { t.Fatalf("ReopenAccessibility: %v", err) }
	apps, err := session.Accessibility.Applications(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "fresh" { t.Fatalf("reopened applications = %+v err=%v", apps, err) }
	if !old.closed { t.Fatal("old accessibility backend was not closed after explicit reopen") }
}

func TestAccessibilityBundleUnavailableIsTyped(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	_, err := session.Accessibility.Focused(context.Background(), accessibility.SnapshotOptions{})
	if err == nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Focused error = %v", err)
	}
}

type accessibilityWindowManager struct{ windows []window.Info }

func (m *accessibilityWindowManager) List(context.Context) ([]window.Info, error) {
	return append([]window.Info(nil), m.windows...), nil
}
func (m *accessibilityWindowManager) IterateWindows(context.Context) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		for _, info := range m.windows {
			if !yield(info, nil) {
				return
			}
		}
	}
}
func (m *accessibilityWindowManager) ActiveTitle(context.Context) (string, error) { return "", nil }
func (m *accessibilityWindowManager) Close() error                                { return nil }

func TestAccessibilityBundleCorrelatesApplicationWithWindow(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "window-1", Title: "Firefox", PID: 77}}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 77}}}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "window-1", WindowTitle: "Firefox"})
	if err != nil || app.PID != 77 {
		t.Fatalf("correlated application = %+v, %v", app, err)
	}
	if _, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "missing"}); !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("missing window error = %v", err)
	}
}

func TestAccessibilityBundleRejectsAmbiguousWindowTitle(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "one", Title: "Firefox"}, {NativeID: "two", Title: "Firefox"}}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 77}}}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	if _, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowTitle: "Firefox"}); !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ambiguous window error = %v", err)
	}
}
