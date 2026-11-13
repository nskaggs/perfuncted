package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type precanceledAccessibilityBackend struct {
	*bundleAccessibilityFake
	calls int
}

func (b *precanceledAccessibilityBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}

func (b *precanceledAccessibilityBackend) Applications(ctx context.Context) ([]accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.Applications(ctx)
}
func (b *precanceledAccessibilityBackend) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	return b.bundleAccessibilityFake.Snapshot(ctx, root, opts)
}
func (b *precanceledAccessibilityBackend) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	return b.bundleAccessibilityFake.Focused(ctx, opts)
}
func (b *precanceledAccessibilityBackend) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	b.calls++
	return b.bundleAccessibilityFake.AtPoint(ctx, x, y)
}
func (b *precanceledAccessibilityBackend) ResolveWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	return b.bundleAccessibilityFake.ResolveWindow(ctx, target)
}
func (b *precanceledAccessibilityBackend) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	return b.bundleAccessibilityFake.ResolveWindowFresh(ctx, target)
}
func (b *precanceledAccessibilityBackend) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.FindApplication(ctx, filter)
}
func (b *precanceledAccessibilityBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.FindApplicationFresh(ctx, filter)
}

// An already-canceled read must not even access a backend that ignores the
// caller's cancellation, regardless of whether its output is later discarded.
func TestAccessibilityReadsNeverDispatchPreCanceledRequests(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.example", ObjectPath: "/app", Generation: 1}
	tests := []struct {
		name string
		read func(*AccessibilityBundle, context.Context) error
	}{
		{"applications", func(b *AccessibilityBundle, c context.Context) error { _, err := b.Applications(c); return err }},
		{"snapshot", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Snapshot(c, root, accessibility.SnapshotOptions{})
			return err
		}},
		{"outline-fallback", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Outline(c, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return err
		}},
		{"find", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Find(c, root, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"find-one-selector", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindOneSelector(c, root, accessibility.Selector{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"focused", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Focused(c, accessibility.SnapshotOptions{})
			return err
		}},
		{"at-point", func(b *AccessibilityBundle, c context.Context) error { _, err := b.AtPoint(c, 2, 3); return err }},
		{"window", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.AccessibilityWindow(c, accessibility.WindowTarget{ID: "test"})
			return err
		}},
		{"fresh-window", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.accessibilityWindow(c, accessibility.WindowTarget{ID: "test"}, true)
			return err
		}},
		{"application", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindApplication(c, accessibility.ApplicationFilter{Name: "Test"})
			return err
		}},
		{"fresh-application", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindApplicationFresh(c, accessibility.ApplicationFilter{Name: "Test"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &precanceledAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1, apps: []accessibility.Application{{Node: accessibility.Node{ID: root, Name: "Test"}}}}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := tc.read(session.Accessibility, ctx)
			if !errors.Is(err, context.Canceled) || backend.calls != 0 {
				t.Fatalf("pre-canceled %s read: err=%v backend calls=%d", tc.name, err, backend.calls)
			}
		})
	}
}
