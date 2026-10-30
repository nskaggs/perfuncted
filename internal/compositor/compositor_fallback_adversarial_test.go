package compositor

import (
	"github.com/nskaggs/perfuncted/internal/env"
	"testing"
)

// Unreachable compositor endpoints must fail closed into documented fallback
// detection, including nested desktop labels and misleading D-Bus addresses.
func TestDetectRuntimeUnreachableSocketFallbacks(t *testing.T) {
	cases := []struct {
		name string
		vars []string
		want Session
	}{
		{"nested KDE", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=ubuntu:KDE"}, KDE},
		{"nested GNOME", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=ubuntu:GNOME"}, GNOME},
		{"nested sway", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=ubuntu:SWAY"}, Wlroots},
		{"hyprland desktop", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=Hyprland"}, Wlroots},
		{"wayfire desktop", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=Wayfire"}, Wlroots},
		{"broken bus", []string{"WAYLAND_DISPLAY=nonexistent", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-test-bus"}, Unknown},
		{"empty bus", []string{"WAYLAND_DISPLAY=nonexistent", "DBUS_SESSION_BUS_ADDRESS="}, Unknown},
		{"signature takes precedence over KDE", []string{"WAYLAND_DISPLAY=nonexistent", "XDG_CURRENT_DESKTOP=KDE", "HYPRLAND_INSTANCE_SIGNATURE=nested"}, Wlroots},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := env.FromEnviron(tc.vars)
			if got := DetectRuntime(rt); got != tc.want {
				t.Fatalf("DetectRuntime(%v)=%v, want %v", tc.vars, got, tc.want)
			}
		})
	}
}
