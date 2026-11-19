package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// After closing an accessibility session, no traversal, resolver, or
// read operation may consult the backend or disclose a stale observation.
func TestAccessibilityClosedSessionRejectsReadsWithoutBackendDispatch(t *testing.T) {
	backend := &bundleAccessibilityFake{gen: 4, apps: []accessibility.Application{{Node: accessibility.Node{Name: "private window"}}}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.example", ObjectPath: "/private", Generation: 4}
	opts := accessibility.SnapshotOptions{}
	actions := []struct {
		name string
		run  func() error
	}{
		{"applications", func() error {
			got, err := session.Accessibility.Applications(ctx)
			if len(got) > 0 {
				t.Errorf("leaked applications: %v", got)
			}
			return err
		}},
		{"snapshot", func() error {
			got, err := session.Accessibility.Snapshot(ctx, id, opts)
			if len(got.Nodes) > 0 {
				t.Errorf("leaked snapshot: %v", got)
			}
			return err
		}},
		{"outline", func() error {
			_, err := session.Accessibility.Outline(ctx, id, opts, accessibility.OutlineOptions{})
			return err
		}},
		{"window scope", func() error {
			got, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "private"})
			if got.WindowID != "" {
				t.Errorf("leaked window scope: %v", got)
			}
			return err
		}},
		{"find", func() error {
			got, err := session.Accessibility.Find(ctx, id, accessibility.Query{}, opts)
			if len(got) > 0 {
				t.Errorf("leaked matches: %v", got)
			}
			return err
		}},
		{"find one", func() error {
			_, err := session.Accessibility.FindOneSelector(ctx, id, accessibility.Selector{}, opts)
			return err
		}},
		{"focused", func() error {
			got, err := session.Accessibility.Focused(ctx, opts)
			if got.Name != "" {
				t.Errorf("leaked focus: %v", got)
			}
			return err
		}},
		{"at point", func() error {
			got, err := session.Accessibility.AtPoint(ctx, 10, 20)
			if got.Name != "" {
				t.Errorf("leaked hit: %v", got)
			}
			return err
		}},
		{"find application", func() error {
			got, err := session.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{Name: "private"})
			if got.Name != "" {
				t.Errorf("leaked application: %v", got)
			}
			return err
		}},
	}
	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("closed session did not reject %s: %v", tc.name, err)
			}
		})
	}
	if backend.findCalls != 0 || backend.freshApplicationFind != 0 || backend.freshWindowResolve != 0 || len(backend.resolvedTargets) > 0 {
		t.Fatalf("closed session dispatched to provider: find=%d fresh=%d windows=%v", backend.findCalls, backend.freshApplicationFind, backend.resolvedTargets)
	}
}
