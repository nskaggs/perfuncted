package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
	"testing"
)

// Selecting an action is itself a mutation. A provider that ignores the
// cancellation context must never receive a pre-canceled action request.
func TestAccessibilityActionReceiptsRejectPreCanceledDispatch(t *testing.T) {
	for _, name := range []string{"by-name", "default"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			backend := &closingActionReceiptBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			previous := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = previous })
			id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/button", Generation: 1}
			var receipt accessibility.Action
			var err error
			if name == "by-name" {
				receipt, err = session.Accessibility.InvokeActionByName(ctx, id, "press")
			} else {
				receipt, err = session.Accessibility.InvokeDefaultAction(ctx, id)
			}
			if !errors.Is(err, context.Canceled) || backend.calls != 0 || receipt.Name != "" {
				t.Fatalf("pre-canceled %s mutation dispatched: receipt=%+v err=%v calls=%d", name, receipt, err, backend.calls)
			}
		})
	}
}

// The compositor's native ID identifies the desired window, but does not
// itself authorize an application identity. Correlation must return a fully
// scoped, generation-bearing AT-SPI application and enforce name filters
// against that *resolved* application rather than unrelated running apps.
func TestAccessibilityFindApplicationWindowCorrelationRequiresFreshIdentityAndMatchingName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		app    accessibility.Application
		gen    uint64
		filter accessibility.ApplicationFilter
		want   error
	}{
		{name: "missing application identity generation", app: accessibility.Application{Node: accessibility.Node{Name: "Editor"}}, gen: 0, filter: accessibility.ApplicationFilter{WindowID: "desktop-window"}, want: accessibility.ErrUnsupportedCorrelation},
		{name: "wrong app name", app: accessibility.Application{Node: accessibility.Node{Name: "Unrelated Browser"}}, gen: 7, filter: accessibility.ApplicationFilter{WindowID: "desktop-window", Name: "Editor"}, want: accessibility.ErrNotFound},
		{name: "matching name case-insensitive", app: accessibility.Application{Node: accessibility.Node{Name: "Text Editor"}}, gen: 7, filter: accessibility.ApplicationFilter{WindowID: "desktop-window", Name: "EDITOR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "desktop-window", Title: "Editor Window", PID: 321}}}
			backend := &bundleAccessibilityFake{apps: []accessibility.Application{tc.app}, gen: tc.gen}
			session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
			defer session.Close()
			app, err := session.Accessibility.FindApplication(context.Background(), tc.filter)
			if tc.want != nil {
				if !errors.Is(err, tc.want) || app.ID.BusName != "" || app.Name != "" {
					t.Fatalf("unsafe correlation: app=%+v err=%v want %v", app, err, tc.want)
				}
			} else if err != nil || app.Name != "Text Editor" || app.ID.Generation != 7 {
				t.Fatalf("valid window correlation refused: app=%+v err=%v", app, err)
			}
			if len(backend.resolvedTargets) != 1 || backend.resolvedTargets[0].ID != "desktop-window" {
				t.Fatalf("incorrect compositor identity used for correlation: %+v", backend.resolvedTargets)
			}
		})
	}
}

func TestAccessibilityFindApplicationWindowResolverFailureCannotSelectAnotherApplication(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "desktop-window", Title: "Editor Window", PID: 321}}}
	backend := &bundleAccessibilityFake{gen: 5}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "desktop-window"})
	if !errors.Is(err, accessibility.ErrNotFound) || app.ID.BusName != "" || len(backend.resolvedTargets) != 1 {
		t.Fatalf("missing window application selected: app=%+v err=%v targets=%+v", app, err, backend.resolvedTargets)
	}
}

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

// Capability advertisements cannot authorize a stream from a backend that
// does not implement EventSource. A rejected event request must not alter the
// underlying provider or accidentally publish a channel with no event owner.
func TestAccessibilityBundleEventsRejectsUnsupportedAndUnavailableSources(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if stream, err := nilBundle.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle admitted events: stream=%v err=%v", stream, err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing backend admitted events: stream=%v err=%v", stream, err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session admitted events: stream=%v err=%v", stream, err)
	}

	provider := newOwnerLifecycleBackend(31, "events", "applications")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	t.Cleanup(func() { _ = session.Close() })
	original := session.Accessibility.backend
	session.Accessibility.backend = &coreOnlyAccessibilityBackend{Backend: provider}
	defer func() { session.Accessibility.backend = original }()
	stream, err := session.Accessibility.Events(context.Background(), accessibility.EventOptions{Buffer: 2})
	if stream != nil || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("provider without EventSource exposed stream=%v err=%v", stream, err)
	}
	if provider.eventCalls.Load() != 0 || provider.closeCalls.Load() != 0 {
		t.Fatalf("unsupported event source dispatched or closed provider: calls=%d closed=%d", provider.eventCalls.Load(), provider.closeCalls.Load())
	}
	session.Accessibility.backend = original
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("rejected event subscription damaged live provider: %v", err)
	}
}

// Generation and invalidation are optional AT-SPI capabilities. A missing,
// typed-nil, or core-only provider must not pretend to expose a live handle
// epoch, and invalidation must never reach a hidden optional implementation.
func TestAccessibilityBundleGenerationAndInvalidationOptionalProviderBoundaries(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Document", ObjectPath: "/document", Generation: 7}
	var absent *AccessibilityBundle
	if absent.Generation() != 0 {
		t.Fatal("nil bundle claimed live generation")
	}
	absent.Invalidate(id)

	var nilFake *bundleAccessibilityFake
	for _, tc := range []struct {
		name    string
		backend accessibility.Backend
	}{
		{name: "unconfigured"},
		{name: "typed nil", backend: nilFake},
		{name: "core only", backend: &coreOnlyAccessibilityBackend{Backend: &bundleAccessibilityFake{gen: 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &AccessibilityBundle{backend: tc.backend}
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported generation=%d, want zero", got)
			}
			bundle.Invalidate(id)
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported invalidation advanced generation=%d", got)
			}
		})
	}
	provider := &bundleAccessibilityFake{gen: 7}
	bundle := &AccessibilityBundle{backend: provider}
	if got := bundle.Generation(); got != 7 {
		t.Fatalf("live provider generation=%d", got)
	}
	bundle.Invalidate(id)
	if got := bundle.Generation(); got != 8 {
		t.Fatalf("invalidation did not advance live generation=%d", got)
	}
}

// A closed session must not expose a live generation identity or let callers
// invalidate a provider that no longer belongs to their session.
func TestAccessibilityGenerationAndInvalidationRejectClosedSession(t *testing.T) {
	backend := &bundleAccessibilityFake{gen: 41}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	original := session.Accessibility.backend
	session.Accessibility.backend = backend
	defer func() { session.Accessibility.backend = original }()
	if got := session.Accessibility.Generation(); got != 41 {
		t.Fatalf("initial generation=%d", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	id := accessibility.NodeID{BusName: "org.example", ObjectPath: "/node", Generation: 41}
	if got := session.Accessibility.Generation(); got != 0 {
		t.Errorf("closed session advertised generation %d", got)
	}
	session.Accessibility.Invalidate(id)
	if backend.gen != 41 {
		t.Errorf("closed session invalidated retired provider to generation %d", backend.gen)
	}
}
