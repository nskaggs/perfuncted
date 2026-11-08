//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// X11 checked mutations may finish during a request's cancellation. Their
// acknowledged transport success must not override the withdrawn operation.
func TestX11MutationsReportCancellationDuringCheckedRequest(t *testing.T) {
	cases := []struct {
		name string
		run  func(*X11Backend, context.Context) error
	}{
		{"activate", func(b *X11Backend, ctx context.Context) error { return b.ActivateByID(ctx, "50") }},
		{"move", func(b *X11Backend, ctx context.Context) error { return b.MoveByID(ctx, "50", 1, 2) }},
		{"resize", func(b *X11Backend, ctx context.Context) error { return b.ResizeByID(ctx, "50", 200, 100) }},
		{"maximize", func(b *X11Backend, ctx context.Context) error { return b.MaximizeByID(ctx, "50") }},
		{"fullscreen", func(b *X11Backend, ctx context.Context) error { return b.FullscreenByID(ctx, "50") }},
		{"unfullscreen", func(b *X11Backend, ctx context.Context) error { return b.UnfullscreenByID(ctx, "50") }},
		{"close", func(b *X11Backend, ctx context.Context) error { return b.CloseWindowByID(ctx, "50") }},
		{"minimize", func(b *X11Backend, ctx context.Context) error { return b.MinimizeByID(ctx, "50") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
				calls++
				cancel()
				return &x11.MockCheckCookie{}
			}
			conn.ConfigureWindowCheckedFunc = func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
				calls++
				cancel()
				return &x11.MockCheckCookie{}
			}
			err := tc.run(b, ctx)
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("late cancellation not reported: err=%v calls=%d", err, calls)
			}
		})
	}
}
