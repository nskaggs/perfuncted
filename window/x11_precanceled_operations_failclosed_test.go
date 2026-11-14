package window

import (
	"context"
	"errors"
	"testing"
)

// A canceled request must not attempt X11 transport operations, including
// when the connection was never opened. Cancellation wins over geometry and
// window ID parsing and is enforced by each public operation entry point.
func TestX11WindowOperationsRejectPreCanceledRequestsBeforeTransport(t *testing.T) {
	var backend X11Backend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []struct {
		name string
		run  func() error
	}{
		{"activate", func() error { return backend.ActivateByID(ctx, "17") }},
		{"move", func() error { return backend.MoveByID(ctx, "17", 20, 30) }},
		{"resize", func() error { return backend.ResizeByID(ctx, "17", 600, 400) }},
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
				t.Fatalf("pre-canceled operation proceeded: %v", err)
			}
		})
	}
}
