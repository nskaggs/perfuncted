package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// Window mutations are scoped to a session-backed stable handle. An absent
// owner must reject every operation before attempting desktop input or
// accidentally forwarding an empty native identifier to a compositor.
func TestUnownedWindowMutationEntryPointsRejectBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	operations := []struct {
		name string
		run  func(*Window) error
	}{
		{"activate", func(w *Window) error { return w.Activate(ctx) }},
		{"move", func(w *Window) error { return w.Move(ctx, 10, 20) }},
		{"resize", func(w *Window) error { return w.Resize(ctx, 640, 480) }},
		{"close", func(w *Window) error { return w.Close(ctx) }},
		{"minimize", func(w *Window) error { return w.Minimize(ctx) }},
		{"maximize", func(w *Window) error { return w.Maximize(ctx) }},
		{"fullscreen", func(w *Window) error { return w.Fullscreen(ctx) }},
		{"unfullscreen", func(w *Window) error { return w.Unfullscreen(ctx) }},
		{"restore", func(w *Window) error { return w.Restore(ctx) }},
	}
	for _, owner := range []struct {
		name   string
		handle *Window
	}{
		{name: "nil handle"},
		{name: "unowned handle", handle: &Window{}},
	} {
		t.Run(owner.name, func(t *testing.T) {
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					if err := op.run(owner.handle); !errors.Is(err, ErrNilSession) {
						t.Fatalf("unowned window operation accepted: %v", err)
					}
				})
			}
		})
	}
}
