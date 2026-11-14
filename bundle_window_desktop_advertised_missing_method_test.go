package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

// An operation name advertised by a compositor is not sufficient to grant a
// dispatch. The concrete optional interface must also exist. This matters
// when an out-of-date or partial backend reports capabilities it cannot serve.
type advertisedDesktopWithoutMethods struct{ *handleWindowManager }

func (*advertisedDesktopWithoutMethods) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}

func TestWindowDesktopAdvertisedButUnimplementedOperationsFailClosed(t *testing.T) {
	backend := &advertisedDesktopWithoutMethods{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	defer session.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"list desktops", func() error {
			got, err := session.Windows.ListDesktops(ctx)
			if len(got) != 0 {
				t.Errorf("unimplemented list leaked %+v", got)
			}
			return err
		}},
		{"switch desktop", func() error { return session.Windows.SwitchDesktop(ctx, "d-1") }},
		{"assign desktop", func() error { return session.Windows.SetWindowDesktopByID(ctx, "w-1", "d-1") }},
		{"keep above", func() error { return session.Windows.SetKeepAboveByID(ctx, "w-1", true) }},
		{"keep below", func() error { return session.Windows.SetKeepBelowByID(ctx, "w-1", true) }},
		{"cursor position", func() error {
			x, y, err := session.Windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("unimplemented pointer leaked coordinates %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.Windows.SetShadeByID(ctx, "w-1", true) }},
		{"tile", func() error { return session.Windows.TileWindowByID(ctx, "w-1", 12, 34) }},
		{"float", func() error { return session.Windows.FloatWindowByID(ctx, "w-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, window.ErrNotSupported) {
				t.Fatalf("unimplemented desktop operation accepted: %v", err)
			}
		})
	}
}
