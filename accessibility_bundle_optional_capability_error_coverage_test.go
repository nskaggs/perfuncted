package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

type restrictedBundleAccessibilityBackend struct {
	accessibility.Backend
	operations []string
}

func (b *restrictedBundleAccessibilityBackend) SupportedOperations() []string {
	return append([]string(nil), b.operations...)
}

type applicationFinderOnlyBundleBackend struct {
	*restrictedBundleAccessibilityBackend
	calls int
}

func (b *applicationFinderOnlyBundleBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return accessibility.Application{}, nil
}

func installRestrictedAccessibilityBackend(t *testing.T, backend accessibility.Backend, manager *accessibilityWindowManager) *Session {
	t.Helper()
	session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
	owner := session.Accessibility.backend
	session.Accessibility.backend = backend
	t.Cleanup(func() {
		session.Accessibility.backend = owner
		_ = session.Close()
	})
	return session
}

func TestAccessibilityBundleRejectsAdvertisedMissingOptionalCapabilities(t *testing.T) {
	ctx := context.Background()
	windowManager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "window-1", Title: "Editor"}}}
	newCore := func(extra ...string) *restrictedBundleAccessibilityBackend {
		base := &bundleAccessibilityFake{gen: 1}
		operations := append([]string(nil), base.SupportedOperations()...)
		operations = append(operations, extra...)
		return &restrictedBundleAccessibilityBackend{Backend: base, operations: operations}
	}

	t.Run("window resolver", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		scope, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "window-1"})
		if scope.WindowID != "" || scope.Root != (accessibility.NodeID{}) || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("window resolution returned scope=%+v err=%v, want unsupported", scope, err)
		}
	})

	t.Run("fresh window resolver", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		scope, err := session.Accessibility.WindowRootFresh(ctx, "window-1")
		if scope.WindowID != "" || scope.Root != (accessibility.NodeID{}) || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("fresh window resolution returned scope=%+v err=%v, want unsupported", scope, err)
		}
	})

	t.Run("application finder", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		app, err := session.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{})
		if app.Node.ID != (accessibility.NodeID{}) || app.Name != "" || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("application lookup returned app=%+v err=%v, want unsupported", app, err)
		}
	})

	t.Run("fresh application finder", func(t *testing.T) {
		backend := newCore()
		finder := &applicationFinderOnlyBundleBackend{restrictedBundleAccessibilityBackend: backend}
		session := installRestrictedAccessibilityBackend(t, finder, windowManager)
		app, err := session.Accessibility.FindApplicationFresh(ctx, accessibility.ApplicationFilter{})
		if app.Node.ID != (accessibility.NodeID{}) || app.Name != "" || !errors.Is(err, accessibility.ErrUnsupported) || finder.calls != 0 {
			t.Fatalf("fresh application lookup returned app=%+v err=%v calls=%d, want unsupported without fallback", app, err, finder.calls)
		}
	})

	t.Run("event source", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		stream, err := session.Accessibility.Events(ctx, accessibility.EventOptions{})
		if stream != nil || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("event subscription returned stream=%v err=%v, want unsupported", stream, err)
		}
	})

	t.Run("automation", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			call func(*AccessibilityBundle) error
		}{
			{"invoke action", func(b *AccessibilityBundle) error { return b.InvokeAction(ctx, accessibility.NodeID{}, 0) }},
			{"default action", func(b *AccessibilityBundle) error {
				_, err := b.InvokeDefaultAction(ctx, accessibility.NodeID{})
				return err
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				backend := newCore("invoke-action", "invoke-default-action")
				session := installRestrictedAccessibilityBackend(t, backend, windowManager)
				if err := tc.call(session.Accessibility); !errors.Is(err, accessibility.ErrUnsupported) {
					t.Fatalf("automation call error = %v, want unsupported", err)
				}
			})
		}
	})
}
