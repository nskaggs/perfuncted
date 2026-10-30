package screen

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"strings"
	"testing"
	"time"
)

func TestOpenRuntimeContextFailsClosedOnUnavailableSyntheticBackends(t *testing.T) {
	for _, tc := range []struct{ name, desktop, want string }{
		{"kde", "KDE", "KDE requires"},
		{"wlroots", "sway", "wlroots compositor"},
		{"unknown", "unrecognised", "unsupported Wayland compositor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := []string{"WAYLAND_DISPLAY=missing-perfuncted-test", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=" + tc.desktop, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/missing-bus"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if b != nil {
				defer b.Close()
				t.Fatalf("unreachable synthetic compositor accepted: %T", b)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}
func TestScreenRuntimeConstructorRespectsCanceledAndAbsentDisplays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"WAYLAND_DISPLAY=missing"}))
	if b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %T %v", b, err)
	}
	b, err = OpenRuntime(env.FromEnviron(nil))
	if b != nil || err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("headless open: %T %v", b, err)
	}
}
