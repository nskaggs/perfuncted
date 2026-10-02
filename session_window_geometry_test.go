package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

// Geometry the backends cannot represent must be refused at the facade, before
// any backend call, so no compositor is asked for a size it will silently
// truncate.
func TestWindowGeometryRejectsUnrepresentableValuesBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invoke  func(*Window) error
		wantErr error
	}{
		{
			name:    "resize negative width",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), -1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize negative height",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), 100, -1) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize zero size",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), 0, 0) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize beyond 16-bit geometry",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), window.MaxDimension+1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "move beyond representable range",
			invoke:  func(w *Window) error { return w.Move(context.Background(), 1<<40, 0) },
			wantErr: ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}

			err = tc.invoke(target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("geometry call = %v, want %v", err, tc.wantErr)
			}
			manager.mu.Lock()
			actions := append([]string(nil), manager.actions...)
			manager.mu.Unlock()
			if len(actions) != 0 {
				t.Fatalf("backend was called for rejected geometry: %v", actions)
			}
		})
	}
}

// Ordinary geometry, including negative positions for a monitor left of the
// primary, must still reach the backend unchanged.
func TestWindowGeometryAcceptsRepresentableValues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invoke     func(*Window) error
		wantAction string
	}{
		{
			name:       "resize",
			invoke:     func(w *Window) error { return w.Resize(context.Background(), 1280, 720) },
			wantAction: "resize:opaque-id",
		},
		{
			name:       "resize to the maximum",
			invoke:     func(w *Window) error { return w.Resize(context.Background(), window.MaxDimension, window.MaxDimension) },
			wantAction: "resize:opaque-id",
		},
		{
			name:       "move to a negative origin",
			invoke:     func(w *Window) error { return w.Move(context.Background(), -1920, -1080) },
			wantAction: "move:opaque-id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}

			if err := tc.invoke(target); err != nil {
				t.Fatalf("geometry call: %v", err)
			}
			manager.mu.Lock()
			actions := append([]string(nil), manager.actions...)
			manager.mu.Unlock()
			if len(actions) != 1 || actions[0] != tc.wantAction {
				t.Fatalf("backend actions = %v, want [%s]", actions, tc.wantAction)
			}
		})
	}
}
