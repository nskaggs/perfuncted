//go:build linux

package window

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Real D-Bus/KWin transport overrides stale session labels only where the
// compositor selection rules authorize it. No actual user desktop is touched.
func TestWindowRuntimeKDESyntheticDBusAndFailClosedSelections(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-wayland-test",
		"XDG_CURRENT_DESKTOP=plasma:KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress,
	})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("KDE runtime failed to open real private-bus KWin service: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*KWinScriptManager); !ok {
		t.Fatalf("KDE runtime selected %T instead of KWin scripting", manager)
	}
	infos, err := manager.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].NativeID != "7" || infos[0].PID != 33 {
		t.Fatalf("KDE runtime list %+v %v", infos, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.List(context.Background()); err == nil {
		t.Fatal("closed KDE manager fabricated data")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if m, err := OpenRuntimeContext(canceled, rt); m != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled KDE runtime unexpectedly opened: %T %v", m, err)
	}
}
func TestWindowRuntimeFailClosedWhenRequestedCompositorIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
		part string
	}{
		{"KDE without bus", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=KDE", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-bus"}, "KDE detected"},
		{"wlroots unavailable", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=sway", "SWAYSOCK=/nonexistent/perfuncted-sway"}, "wlroots"},
		{"X11 no display", []string{"XDG_SESSION_TYPE=x11"}, "no display"},
		{"unknown Wayland", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=unrecognized"}, "unsupported Wayland"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, err := OpenRuntimeContext(context.Background(), env.FromEnviron(tc.vars))
			if manager != nil || err == nil || !strings.Contains(err.Error(), tc.part) {
				t.Fatalf("unavailable %s opened manager %T, error %v", tc.name, manager, err)
			}
		})
	}
}
