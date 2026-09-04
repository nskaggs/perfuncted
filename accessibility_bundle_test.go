package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
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
