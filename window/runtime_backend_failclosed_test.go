package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"strings"
	"testing"
	"time"
)

func TestWindowOpenRuntimeContextFailsClosedOnUnavailableSyntheticBackends(t *testing.T) {
	for _, tc := range []struct{ name, desktop, want string }{
		{"kde", "KDE", "KWin scripting unavailable"},
		{"wlroots", "sway", "no window manager"},
		{"unknown", "unrecognised", "unsupported Wayland compositor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := []string{"WAYLAND_DISPLAY=missing-perfuncted-test", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=" + tc.desktop, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/missing-bus"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			manager, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if manager != nil {
				defer manager.Close()
				t.Fatalf("unreachable compositor accepted: %T", manager)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
}
func TestWindowRuntimeConstructorRespectsCancellationAndAbsentDisplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"WAYLAND_DISPLAY=missing"}))
	if manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled manager: %T %v", manager, err)
	}
	manager, err = OpenRuntime(env.FromEnviron(nil))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("headless manager: %T %v", manager, err)
	}
}
