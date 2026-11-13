package perfuncted

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A missing accessibility service must never expose a value, even when callers
// invoke discovery operations directly on a nil bundle. The absence of any
// session also must not trigger a provider operation or panic.
func TestNilAccessibilityBundleReadsFailClosed(t *testing.T) {
	var bundle *AccessibilityBundle
	ctx := context.Background()
	root := accessibility.NodeID{BusName: "org.example.Application", ObjectPath: "/app", Generation: 7}
	selector := accessibility.Selector{Name: "Save", Role: "button"}
	query := accessibility.Query{Name: "Save", Role: "button"}
	for _, tc := range []struct {
		name string
		call func() (bool, error)
	}{
		{"applications", func() (bool, error) { v, e := bundle.Applications(ctx); return len(v) > 0, e }},
		{"snapshot", func() (bool, error) {
			v, e := bundle.Snapshot(ctx, root, accessibility.SnapshotOptions{})
			return len(v.Nodes) > 0 || v.Root.ID.BusName != "", e
		}},
		{"outline", func() (bool, error) {
			v, e := bundle.Outline(ctx, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return v.Root.ID.BusName != "", e
		}},
		{"window", func() (bool, error) {
			v, e := bundle.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "win"})
			return v.Root.BusName != "", e
		}},
		{"fresh-window", func() (bool, error) {
			v, e := bundle.accessibilityWindow(ctx, accessibility.WindowTarget{ID: "win"}, true)
			return v.Root.BusName != "", e
		}},
		{"find", func() (bool, error) {
			v, e := bundle.Find(ctx, root, query, accessibility.SnapshotOptions{})
			return len(v) > 0, e
		}},
		{"find-one", func() (bool, error) {
			v, e := bundle.FindOne(ctx, root, query, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"find-one-selector", func() (bool, error) {
			v, e := bundle.FindOneSelector(ctx, root, selector, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"focused", func() (bool, error) {
			v, e := bundle.Focused(ctx, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"at-point", func() (bool, error) { v, e := bundle.AtPoint(ctx, 1, 2); return v.ID.BusName != "", e }},
		{"application", func() (bool, error) {
			v, e := bundle.FindApplication(ctx, accessibility.ApplicationFilter{Name: "Editor"})
			return v.ID.BusName != "", e
		}},
		{"fresh-application", func() (bool, error) {
			v, e := bundle.FindApplicationFresh(ctx, accessibility.ApplicationFilter{Name: "Editor"})
			return v.ID.BusName != "", e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exposed, err := tc.call()
			if exposed || err == nil || err.Error() != "session is nil" {
				t.Fatalf("nil backend read: exposed=%t err=%v", exposed, err)
			}
		})
	}
}
