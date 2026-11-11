package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

func (m *desktopMutationRaceManager) ListDesktops(context.Context) ([]window.DesktopInfo, error) {
	err := m.finish("list")
	return []window.DesktopInfo{{ID: "desktop-id", Position: 1, Current: true}}, err
}
func (m *desktopMutationRaceManager) CursorPosition(context.Context) (int, int, error) {
	err := m.finish("cursor")
	return 13, 17, err
}

// The desktop read result is an authorization-bound observation: cancellation
// must remain visible even when the compositor returned a separate error.
func TestDesktopReadRetainsConcurrentCancellationAndBackendFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*WindowBundle, context.Context) error
	}{
		{"list", func(b *WindowBundle, ctx context.Context) error { _, err := b.ListDesktops(ctx); return err }},
		{"cursor", func(b *WindowBundle, ctx context.Context) error { _, _, err := b.CursorPosition(ctx); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backendErr := errors.New("synthetic backend read failure")
			manager := &desktopMutationRaceManager{after: cancel, resultErr: backendErr}
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			defer session.Close()
			err := tc.invoke(session.Windows, ctx)
			if manager.seen != tc.name {
				t.Fatalf("wrong backend call: %q", manager.seen)
			}
			if !errors.Is(err, context.Canceled) || !errors.Is(err, backendErr) {
				t.Fatalf("concurrent cancellation and backend failure not preserved: %v", err)
			}
		})
	}
}

func TestDesktopReadSuccessCancellationAndSessionClosure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*WindowBundle, context.Context) error
	}{
		{"list", func(b *WindowBundle, ctx context.Context) error {
			ds, err := b.ListDesktops(ctx)
			if err == nil && (len(ds) != 1 || ds[0].ID != "desktop-id" || !ds[0].Current) {
				return errors.New("lost returned desktop identity")
			}
			return err
		}},
		{"cursor", func(b *WindowBundle, ctx context.Context) error {
			x, y, err := b.CursorPosition(ctx)
			if err == nil && (x != 13 || y != 17) {
				return errors.New("lost compositor cursor position")
			}
			return err
		}},
	} {
		for _, mode := range []string{"success", "late-cancellation", "session-closed"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				manager := &desktopMutationRaceManager{}
				session := NewSessionForTesting(nil, nil, manager, nil, nil)
				defer session.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch mode {
				case "late-cancellation":
					manager.after = cancel
				case "session-closed":
					manager.after = func() { _ = session.Close() }
				}
				err := tc.invoke(session.Windows, ctx)
				if manager.seen != tc.name {
					t.Fatalf("unexpected backend call: %q", manager.seen)
				}
				switch mode {
				case "success":
					if err != nil {
						t.Fatalf("read failed: %v", err)
					}
				case "late-cancellation":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled read acknowledged success: %v", err)
					}
				case "session-closed":
					if !errors.Is(err, ErrSessionClosed) {
						t.Fatalf("closed session acknowledged read: %v", err)
					}
				}
			})
		}
	}
}
