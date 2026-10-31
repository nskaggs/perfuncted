package perfuncted_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// A connected but inert Wayland socket must not trap public context-aware
// backend openers during compositor detection. No actual desktop or input
// device is used: these calls must abort before backend initialization.
func TestBackendOpenersAbortWedgedCompositorDetectionAtCallerDeadline(t *testing.T) {
	t.Setenv("PF_FORCE_INPUT", "")
	dir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-hung"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-hung", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=SWAY"})
	for _, tc := range []struct {
		name string
		open func(context.Context) error
	}{
		{"input", func(ctx context.Context) error {
			b, err := input.OpenRuntimeContext(ctx, rt, 1280, 720)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
		{"screen", func(ctx context.Context) error {
			b, err := screen.OpenRuntimeContext(ctx, rt)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
		{"window", func(ctx context.Context) error {
			b, err := window.OpenRuntimeContext(ctx, rt)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			started := time.Now()
			if err := tc.open(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wedged compositor returned %v, want caller deadline", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("backend opener took %s beyond caller deadline", elapsed)
			}
		})
	}
}
