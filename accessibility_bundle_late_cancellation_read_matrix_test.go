package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// This backend simulates an AT-SPI provider that completes a successful read
// after the caller has canceled. Its values must never be disclosed by any of
// the high-level query entry points, including cached and fresh resolution.
type lateCanceledAccessibilityReadBackend struct {
	*bundleAccessibilityFake
	cancel context.CancelFunc
	calls  int
	root   accessibility.NodeID
}

func (b *lateCanceledAccessibilityReadBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *lateCanceledAccessibilityReadBackend) finish() { b.calls++; b.cancel() }
func (b *lateCanceledAccessibilityReadBackend) Applications(context.Context) ([]accessibility.Application, error) {
	b.finish()
	return []accessibility.Application{{Node: accessibility.Node{ID: b.root, Name: "private-app"}}}, nil
}
func (b *lateCanceledAccessibilityReadBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.finish()
	root := accessibility.Node{ID: b.root, Role: "application"}
	return accessibility.Snapshot{Root: root, Nodes: []accessibility.Node{root, {ID: accessibility.NodeID{BusName: b.root.BusName, ObjectPath: "/private", Generation: b.root.Generation}, Parent: b.root, Name: "private-button", Role: "button"}}, Generation: b.root.Generation}, nil
}
func (b *lateCanceledAccessibilityReadBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.finish()
	return accessibility.Node{ID: b.root, Name: "private-focused"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	b.finish()
	return accessibility.Node{ID: b.root, Name: "private-point"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) ResolveWindow(context.Context, accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.finish()
	return accessibility.WindowScope{Root: b.root, WindowID: "private-window"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) ResolveWindowFresh(c context.Context, t accessibility.WindowTarget) (accessibility.WindowScope, error) {
	return b.ResolveWindow(c, t)
}
func (b *lateCanceledAccessibilityReadBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.finish()
	return accessibility.Application{Node: accessibility.Node{ID: b.root, Name: "private-app"}}, nil
}
func (b *lateCanceledAccessibilityReadBackend) FindApplicationFresh(c context.Context, f accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.FindApplication(c, f)
}

func TestAccessibilityReadResultsAreDiscardedAfterLateCallerCancellation(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.private.Test", ObjectPath: "/app", Generation: 1}
	selector := accessibility.Selector{Name: "private-button", Role: "button"}
	tests := []struct {
		name string
		read func(*AccessibilityBundle, context.Context) (bool, error)
	}{
		{"applications", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Applications(c)
			return len(a) != 0, e
		}},
		{"snapshot", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Snapshot(c, root, accessibility.SnapshotOptions{})
			return len(a.Nodes) != 0, e
		}},
		{"outline-fallback", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Outline(c, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return a.Root.ID.BusName != "" || len(a.Warnings) != 0, e
		}},
		{"find", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Find(c, root, accessibility.Query{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
			return len(a) != 0, e
		}},
		{"find-one", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindOneSelector(c, root, selector, accessibility.SnapshotOptions{})
			return a.Name != "", e
		}},
		{"focused", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Focused(c, accessibility.SnapshotOptions{})
			return a.Name != "", e
		}},
		{"at-point", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.AtPoint(c, 5, 6)
			return a.Name != "", e
		}},
		{"window", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.AccessibilityWindow(c, accessibility.WindowTarget{ID: "private-window"})
			return a.Root.BusName != "", e
		}},
		{"fresh-window", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.accessibilityWindow(c, accessibility.WindowTarget{ID: "private-window"}, true)
			return a.Root.BusName != "", e
		}},
		{"application", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindApplication(c, accessibility.ApplicationFilter{Name: "private"})
			return a.Name != "", e
		}},
		{"fresh-application", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindApplicationFresh(c, accessibility.ApplicationFilter{Name: "private"})
			return a.Name != "", e
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &lateCanceledAccessibilityReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, cancel: cancel, root: root}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			exposed, err := tc.read(session.Accessibility, ctx)
			if !errors.Is(err, context.Canceled) || exposed || backend.calls != 1 {
				t.Fatalf("late-canceled %s: exposed=%v err=%v backend calls=%d", tc.name, exposed, err, backend.calls)
			}
		})
	}
}
