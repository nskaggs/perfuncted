package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
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

// A reconnect request must not manufacture a backend from an absent, closed,
// or non-reconnectable accessibility provider. In particular, advertising the
// capability does not authorize casting an arbitrary backend to its owner.
func TestAccessibilityBundleReopenRejectsAbsentClosedOrUnownedBackend(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if err := nilBundle.Reopen(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle reopened: %v", err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("absent provider reopened: %v", err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session reopened: %v", err)
	}

	owned := newOwnerLifecycleBackend(8, "applications", "reopen")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, owned)
	t.Cleanup(func() { _ = session.Close() })
	owner := session.Accessibility.backend
	direct := &coreOnlyAccessibilityBackend{Backend: owned}
	session.Accessibility.backend = direct
	defer func() { session.Accessibility.backend = owner }()
	if err := session.Accessibility.Reopen(context.Background()); !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("unowned backend reconnect: %v", err)
	}
	if owned.closeCalls.Load() != 0 {
		t.Fatal("unsupported reconnect unexpectedly closed live owner backend")
	}
	if !session.Capability(CapabilityAccessibility).Available {
		t.Fatal("unsupported reconnect disabled still-owned capability")
	}
	session.Accessibility.backend = owner
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("unsupported request corrupted original backend: %v", err)
	}
}
