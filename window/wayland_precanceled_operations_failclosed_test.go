package window

import (
	"context"
	"errors"
	"testing"
)

// Revoked Wayland requests must fail before touching a display or a foreign
// toplevel handle. Keep the transport absent so an accidental call cannot be
// mistaken for an ordinary compositor rejection.
func TestWaylandWindowOperationsRejectPreCanceledRequestsBeforeTransport(t *testing.T) {
	var backend WaylandWindowManager
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []struct {
		name string
		run  func() error
	}{
		{"activate", func() error { return backend.ActivateByID(ctx, "17") }},
		{"close", func() error { return backend.CloseWindowByID(ctx, "17") }},
		{"minimize", func() error { return backend.MinimizeByID(ctx, "17") }},
		{"maximize", func() error { return backend.MaximizeByID(ctx, "17") }},
		{"fullscreen", func() error { return backend.FullscreenByID(ctx, "17") }},
		{"unfullscreen", func() error { return backend.UnfullscreenByID(ctx, "17") }},
		{"restore", func() error { return backend.RestoreByID(ctx, "17") }},
		{"info", func() error { _, err := backend.InfoByID(ctx, "17"); return err }},
	}
	for _, tc := range operations {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled Wayland request proceeded: %v", err)
			}
		})
	}
}
