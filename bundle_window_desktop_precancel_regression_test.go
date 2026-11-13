package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

type countingDesktopBackend struct {
	*handleWindowManager
	calls []string
}

func (b *countingDesktopBackend) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}
func (b *countingDesktopBackend) ListDesktops(context.Context) ([]window.DesktopInfo, error) {
	b.calls = append(b.calls, "list")
	return []window.DesktopInfo{{}}, nil
}
func (b *countingDesktopBackend) SwitchDesktop(context.Context, string) error {
	b.calls = append(b.calls, "switch")
	return nil
}
func (b *countingDesktopBackend) SetWindowDesktopByID(context.Context, string, string) error {
	b.calls = append(b.calls, "set-desktop")
	return nil
}
func (b *countingDesktopBackend) SetKeepAboveByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "above")
	return nil
}
func (b *countingDesktopBackend) SetKeepBelowByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "below")
	return nil
}
func (b *countingDesktopBackend) CursorPosition(context.Context) (int, int, error) {
	b.calls = append(b.calls, "cursor")
	return 123, 456, nil
}
func (b *countingDesktopBackend) SetShadeByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "shade")
	return nil
}
func (b *countingDesktopBackend) TileWindowByID(context.Context, string, int, int) error {
	b.calls = append(b.calls, "tile")
	return nil
}
func (b *countingDesktopBackend) FloatWindowByID(context.Context, string) error {
	b.calls = append(b.calls, "float")
	return nil
}

// A canceled request cannot authorize even a transient compositor call.
func TestWindowDesktopFacadePreCanceledRequestsDoNotDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &countingDesktopBackend{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := []struct {
		name string
		run  func() error
	}{
		{"list", func() error {
			items, err := session.Windows.ListDesktops(ctx)
			if len(items) != 0 {
				t.Errorf("canceled desktop list leaked %v", items)
			}
			return err
		}},
		{"switch", func() error { return session.Windows.SwitchDesktop(ctx, "1") }},
		{"set-desktop", func() error { return session.Windows.SetWindowDesktopByID(ctx, "5", "1") }},
		{"above", func() error { return session.Windows.SetKeepAboveByID(ctx, "5", true) }},
		{"below", func() error { return session.Windows.SetKeepBelowByID(ctx, "5", true) }},
		{"cursor", func() error {
			x, y, err := session.Windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("canceled cursor leaked coordinates %d %d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.Windows.SetShadeByID(ctx, "5", true) }},
		{"tile", func() error { return session.Windows.TileWindowByID(ctx, "5", 42, 23) }},
		{"float", func() error { return session.Windows.FloatWindowByID(ctx, "5") }},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			before := len(backend.calls)
			err := action.run()
			if !errors.Is(err, context.Canceled) || len(backend.calls) != before {
				t.Fatalf("canceled action returned %v; calls=%v", err, backend.calls)
			}
		})
	}
}
