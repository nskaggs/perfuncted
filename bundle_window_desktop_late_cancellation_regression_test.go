package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

type desktopMutationRaceManager struct {
	window.Manager
	after     func()
	seen      string
	resultErr error
}

func (m *desktopMutationRaceManager) Close() error { return nil }
func (m *desktopMutationRaceManager) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}
func (m *desktopMutationRaceManager) finish(op string) error {
	m.seen = op
	if m.after != nil {
		m.after()
	}
	return m.resultErr
}
func (m *desktopMutationRaceManager) SwitchDesktop(_ context.Context, id string) error {
	if id != "desktop-id" {
		return errors.New("unexpected desktop ID")
	}
	return m.finish("switch")
}
func (m *desktopMutationRaceManager) SetWindowDesktopByID(_ context.Context, id, desktop string) error {
	if id != "window-id" || desktop != "desktop-id" {
		return errors.New("unexpected desktop assignment")
	}
	return m.finish("assign")
}
func (m *desktopMutationRaceManager) SetKeepAboveByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected above args")
	}
	return m.finish("above")
}
func (m *desktopMutationRaceManager) SetKeepBelowByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected below args")
	}
	return m.finish("below")
}
func (m *desktopMutationRaceManager) SetShadeByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected shade args")
	}
	return m.finish("shade")
}
func (m *desktopMutationRaceManager) TileWindowByID(_ context.Context, id string, x, y int) error {
	if id != "window-id" || x != 13 || y != 17 {
		return errors.New("unexpected tile args")
	}
	return m.finish("tile")
}
func (m *desktopMutationRaceManager) FloatWindowByID(_ context.Context, id string) error {
	if id != "window-id" {
		return errors.New("unexpected float args")
	}
	return m.finish("float")
}

func TestDesktopMutationsDoNotAcknowledgeCanceledOperation(t *testing.T) {
	calls := []struct {
		name   string
		invoke func(*WindowBundle, context.Context) error
	}{
		{"switch", func(b *WindowBundle, c context.Context) error { return b.SwitchDesktop(c, "desktop-id") }},
		{"assign", func(b *WindowBundle, c context.Context) error {
			return b.SetWindowDesktopByID(c, "window-id", "desktop-id")
		}},
		{"above", func(b *WindowBundle, c context.Context) error { return b.SetKeepAboveByID(c, "window-id", true) }},
		{"below", func(b *WindowBundle, c context.Context) error { return b.SetKeepBelowByID(c, "window-id", true) }},
		{"shade", func(b *WindowBundle, c context.Context) error { return b.SetShadeByID(c, "window-id", true) }},
		{"tile", func(b *WindowBundle, c context.Context) error { return b.TileWindowByID(c, "window-id", 13, 17) }},
		{"float", func(b *WindowBundle, c context.Context) error { return b.FloatWindowByID(c, "window-id") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager := &desktopMutationRaceManager{after: cancel}
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			defer session.Close()
			err := tc.invoke(session.Windows, ctx)
			if manager.seen != tc.name {
				t.Fatalf("backend not invoked: got %q", manager.seen)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("backend canceled during operation, returned %v, want context.Canceled", err)
			}
		})
	}
}

// Successful backend completion must remain successful, while concurrent
// transport failure and cancellation must retain both error identities.
func TestDesktopMutationCompletionAndBackendFailureContracts(t *testing.T) {
	calls := []struct {
		name   string
		invoke func(*WindowBundle, context.Context) error
	}{
		{"switch", func(b *WindowBundle, c context.Context) error { return b.SwitchDesktop(c, "desktop-id") }},
		{"assign", func(b *WindowBundle, c context.Context) error {
			return b.SetWindowDesktopByID(c, "window-id", "desktop-id")
		}},
		{"above", func(b *WindowBundle, c context.Context) error { return b.SetKeepAboveByID(c, "window-id", true) }},
		{"below", func(b *WindowBundle, c context.Context) error { return b.SetKeepBelowByID(c, "window-id", true) }},
		{"shade", func(b *WindowBundle, c context.Context) error { return b.SetShadeByID(c, "window-id", true) }},
		{"tile", func(b *WindowBundle, c context.Context) error { return b.TileWindowByID(c, "window-id", 13, 17) }},
		{"float", func(b *WindowBundle, c context.Context) error { return b.FloatWindowByID(c, "window-id") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"success", "backend-error", "cancel-and-error", "session-closed"} {
				t.Run(mode, func(t *testing.T) {
					manager := &desktopMutationRaceManager{}
					session := NewSessionForTesting(nil, nil, manager, nil, nil)
					defer session.Close()
					backendErr := errors.New("synthetic backend failure")
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch mode {
					case "backend-error":
						manager.resultErr = backendErr
					case "cancel-and-error":
						manager.resultErr = backendErr
						manager.after = cancel
					case "session-closed":
						manager.after = func() { _ = session.Close() }
					}
					err := tc.invoke(session.Windows, ctx)
					if manager.seen != tc.name {
						t.Fatalf("wrong backend call: %q", manager.seen)
					}
					switch mode {
					case "success":
						if err != nil {
							t.Fatalf("successful operation: %v", err)
						}
					case "backend-error":
						if !errors.Is(err, backendErr) {
							t.Fatalf("lost backend error: %v", err)
						}
					case "cancel-and-error":
						if !errors.Is(err, backendErr) || !errors.Is(err, context.Canceled) {
							t.Fatalf("lost failure or cancellation: %v", err)
						}
					case "session-closed":
						if !errors.Is(err, ErrSessionClosed) {
							t.Fatalf("closed session acknowledged success: %v", err)
						}
					}
				})
			}
		})
	}
}
