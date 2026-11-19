package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// Once a session closes, desktop operations must reject the caller before
// executing any compositor request, even when the backend advertises support.
func TestWindowDesktopClosedSessionRevokesAllOperations(t *testing.T) {
	backend := &countingDesktopBackend{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list desktops", func() error {
			list, err := session.Windows.ListDesktops(ctx)
			if len(list) != 0 {
				t.Errorf("closed session leaked desktop list: %v", list)
			}
			return err
		}},
		{"switch desktop", func() error { return session.Windows.SwitchDesktop(ctx, "desktop-1") }},
		{"set desktop", func() error { return session.Windows.SetWindowDesktopByID(ctx, "window-1", "desktop-1") }},
		{"keep above", func() error { return session.Windows.SetKeepAboveByID(ctx, "window-1", true) }},
		{"keep below", func() error { return session.Windows.SetKeepBelowByID(ctx, "window-1", true) }},
		{"cursor position", func() error {
			x, y, err := session.Windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("closed session leaked cursor position %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.Windows.SetShadeByID(ctx, "window-1", true) }},
		{"tile", func() error { return session.Windows.TileWindowByID(ctx, "window-1", 10, 20) }},
		{"float", func() error { return session.Windows.FloatWindowByID(ctx, "window-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(backend.calls)
			if err := tc.run(); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("closed session desktop operation returned %v", err)
			}
			if len(backend.calls) != before {
				t.Fatalf("closed session dispatched compositor request: %v", backend.calls[before:])
			}
		})
	}
}
