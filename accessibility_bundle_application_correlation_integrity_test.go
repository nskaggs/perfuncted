package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

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
