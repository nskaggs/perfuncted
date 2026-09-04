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
