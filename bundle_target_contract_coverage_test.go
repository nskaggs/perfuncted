package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

func TestManagedWindowResolverRejectsMissingScopeFailuresAndAmbiguity(t *testing.T) {
	if selected, err := (*AccessibilityBundle)(nil).resolveManagedWindow(context.Background(), "", "", "test"); selected != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle resolution = %v, %v; want nil and ErrNilSession", selected, err)
	}
	if selected, err := (&AccessibilityBundle{}).resolveManagedWindow(context.Background(), "", "", "test"); selected != nil || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("bundle without managed windows = %v, %v; want unsupported", selected, err)
	}

	manager := newHandleWindowManager(
		window.Info{NativeID: "one", Title: "Shared Editor"},
		window.Info{NativeID: "two", Title: "Shared Editor"},
	)
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	selected, err := session.Accessibility.resolveManagedWindow(context.Background(), "", " Shared Editor ", "test")
	if selected != nil || !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ambiguous title selected=%v err=%v; want no target and ErrAmbiguous", selected, err)
	}

	// A valid native ID must not override contradictory title evidence.
	selected, err = session.Accessibility.resolveManagedWindow(context.Background(), "one", "Other Person Private Window", "test")
	if selected != nil || !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("conflicting managed window title selected=%v err=%v; want no target and ErrNotFound", selected, err)
	}

	providerFailure := errors.New("synthetic compositor window listing failure")
	manager.setError(providerFailure)
	selected, err = session.Accessibility.resolveManagedWindow(context.Background(), "one", "", "test")
	if selected != nil || !errors.Is(err, providerFailure) {
		t.Fatalf("window listing failure selected=%v err=%v; want wrapped provider error", selected, err)
	}
}

func TestWindowTargetUsesPIDHintOnlyWhenManagedMetadataOmitsPID(t *testing.T) {
	selected := &Window{
		id:       WindowID{native: "managed-42"},
		snapshot: window.Info{Title: "Editor", AppID: "org.example.Editor", X: 10, Y: 20, W: 300, H: 200, Active: true},
	}
	target := windowTargetFor(selected, 42)
	if target.ID != "managed-42" || target.PID != 0 || target.PIDHint != 42 || target.Title != "Editor" || target.AppID != "org.example.Editor" || target.Bounds != (accessibility.Rect{X: 10, Y: 20, Width: 300, Height: 200}) || !target.Active {
		t.Fatalf("window correlation target lost managed metadata or PID hint: %+v", target)
	}

	selected.snapshot.PID = 84
	target = windowTargetFor(selected, 42)
	if target.PID != 84 || target.PIDHint != 0 {
		t.Fatalf("caller PID hint overrode compositor PID: %+v", target)
	}
}
