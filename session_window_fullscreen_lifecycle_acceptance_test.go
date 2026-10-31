package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

type windowFullscreenFailureManager struct {
	*handleWindowManager
	actionErr error
}

func (m *windowFullscreenFailureManager) FullscreenByID(ctx context.Context, id string) error {
	if m.actionErr != nil {
		return m.actionErr
	}
	return m.handleWindowManager.FullscreenByID(ctx, id)
}
func (m *windowFullscreenFailureManager) UnfullscreenByID(ctx context.Context, id string) error {
	if m.actionErr != nil {
		return m.actionErr
	}
	return m.handleWindowManager.UnfullscreenByID(ctx, id)
}

func TestWindowFullscreenAndUnfullscreenRequireStableWindowIDAndLiveSession(t *testing.T) {
	manager := &windowFullscreenFailureManager{handleWindowManager: newHandleWindowManager(window.Info{NativeID: "private-101", Title: "Synthetic Editor"})}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
	if err != nil {
		t.Fatalf("find stable-ID window: %v", err)
	}
	if err := handle.Fullscreen(context.Background()); err != nil {
		t.Fatalf("fullscreen stable ID: %v", err)
	}
	if err := handle.Unfullscreen(context.Background()); err != nil {
		t.Fatalf("unfullscreen stable ID: %v", err)
	}
	manager.mu.Lock()
	calls := append([]string(nil), manager.actions...)
	manager.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"fullscreen:private-101", "unfullscreen:private-101"}) {
		t.Fatalf("window action used title or stale ID: %v", calls)
	}
	failure := errors.New("compositor rejected fullscreen")
	manager.actionErr = failure
	if err := handle.Fullscreen(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("fullscreen backend failure lost: %v", err)
	}
	if err := handle.Unfullscreen(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("unfullscreen backend failure lost: %v", err)
	}
	manager.actionErr = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handle.Fullscreen(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fullscreen accepted: %v", err)
	}
	if err := handle.Unfullscreen(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled unfullscreen accepted: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Fullscreen(context.Background()); err == nil {
		t.Fatal("closed session fullscreen accepted")
	}
	if err := handle.Unfullscreen(context.Background()); err == nil {
		t.Fatal("closed session unfullscreen accepted")
	}
}

func TestAllWindowOperationsRejectCanceledContextBeforeBackendDispatch(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-101", Title: "Synthetic Editor"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"activate", handle.Activate},
		{"move", func(ctx context.Context) error { return handle.Move(ctx, 11, 23) }},
		{"resize", func(ctx context.Context) error { return handle.Resize(ctx, 100, 80) }},
		{"close", handle.Close},
		{"minimize", handle.Minimize},
		{"maximize", handle.Maximize},
		{"restore", handle.Restore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(canceled); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled action dispatched: %v", err)
			}
		})
	}
	if _, err := handle.Info(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled metadata refresh returned %v", err)
	}
	manager.mu.Lock()
	actions := append([]string(nil), manager.actions...)
	manager.mu.Unlock()
	if len(actions) != 0 {
		t.Fatalf("cancelled operations reached window backend: %v", actions)
	}
}
