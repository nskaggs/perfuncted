package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
	"strings"
	"testing"
)

func TestX11MoveRejectsUnrepresentableSignedWireCoordinatesBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"x greater than int16", 32768, 42}, {"y greater than int16", 42, 32768},
		{"x less than int16", -32769, 42}, {"y less than int16", 42, -32769},
		{"x wraps uint32", 1 << 33, 42}, {"negative x wraps uint32", -(1 << 33), 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, true, "A")
			mutated := 0
			conn.ConfigureWindowCheckedFunc = func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
				mutated++
				return &x11.MockCheckCookie{}
			}
			err := b.MoveByID(context.Background(), "50", tc.x, tc.y)
			if err == nil || mutated != 0 {
				t.Fatalf("invalid X11 coordinate %d,%d dispatched %d times: %v", tc.x, tc.y, mutated, err)
			}
		})
	}
}

func TestX11MoveAllowsSignedWireBoundaryCoordinates(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "A")
	var moves [][2]int
	conn.ConfigureWindowCheckedFunc = func(_ xproto.Window, _ uint16, values []uint32) x11.ConfigureWindowCookie {
		moves = append(moves, [2]int{int(int32(values[0])), int(int32(values[1]))})
		return &x11.MockCheckCookie{}
	}
	for _, tc := range [][2]int{{-32768, 32767}, {32767, -32768}, {0, 0}} {
		if err := b.MoveByID(context.Background(), "50", tc[0], tc[1]); err != nil {
			t.Fatalf("valid signed x11 coordinate %v rejected: %v", tc, err)
		}
	}
	if len(moves) != 3 || moves[0] != ([2]int{-32768, 32767}) || moves[1] != ([2]int{32767, -32768}) || moves[2] != ([2]int{0, 0}) {
		t.Fatalf("valid signed positions were not preserved: %v", moves)
	}
}

// An unavailable X11 backend must not panic or try to access connection
// state when an application attempts operations during startup/shutdown.
func TestX11NilBackendRejectsAdmittedOperationsWithoutPanic(t *testing.T) {
	var backend *X11Backend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeated nil backend Close: %v", err)
	}
	if title, err := backend.ActiveTitle(context.Background()); title != "" || err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend leaked title or did not fail: title=%q err=%v", title, err)
	}
	if err := backend.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend Sync succeeded: %v", err)
	}
}

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
		{"list", func() error { _, err := backend.List(ctx); return err }},
		{"active title", func() error { _, err := backend.ActiveTitle(ctx); return err }},
		{"sync", func() error { return backend.Sync(ctx) }},
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
