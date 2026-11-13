package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type closingAccessibilityIdentityBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingAccessibilityIdentityBackend) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.WindowScope{WindowID: target.ID, Root: accessibility.NodeID{BusName: "org.private", ObjectPath: "/window", Generation: 1}}, nil
}
func (b *closingAccessibilityIdentityBackend) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	return b.ResolveWindow(ctx, target)
}
func (b *closingAccessibilityIdentityBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Application{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.private", ObjectPath: "/app", Generation: 1}, Name: "private-app"}}, nil
}
func (b *closingAccessibilityIdentityBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.FindApplication(ctx, filter)
}

// A provider result cannot authorize a window or application identity after
// the session closes while that provider is executing.
func TestAccessibilityIdentityReadsDiscardResultsAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"window", "fresh-window", "application", "fresh-application"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingAccessibilityIdentityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var err error
			var leaked string
			switch name {
			case "window", "fresh-window":
				var scope accessibility.WindowScope
				if name == "window" {
					scope, err = session.Accessibility.AccessibilityWindow(context.Background(), accessibility.WindowTarget{ID: "win", Title: "Editor"})
				} else {
					scope, err = session.Accessibility.accessibilityWindow(context.Background(), accessibility.WindowTarget{ID: "win", Title: "Editor"}, true)
				}
				leaked = scope.Root.BusName
			case "application", "fresh-application":
				var app accessibility.Application
				if name == "application" {
					app, err = session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{Name: "private"})
				} else {
					app, err = session.Accessibility.FindApplicationFresh(context.Background(), accessibility.ApplicationFilter{Name: "private"})
				}
				leaked = app.Name
			}
			if !errors.Is(err, ErrSessionClosed) || leaked != "" || backend.calls != 1 {
				t.Fatalf("identity leaked: result=%q err=%v calls=%d", leaked, err, backend.calls)
			}
		})
	}
}
