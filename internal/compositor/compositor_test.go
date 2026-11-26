package compositor

import (
	"github.com/nskaggs/perfuncted/v2/internal/env"
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

func TestSessionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Session
		want string
	}{
		{name: "x11", in: X11, want: "X11"},
		{name: "kde", in: KDE, want: "KDE Plasma Wayland"},
		{name: "wlroots", in: Wlroots, want: "wlroots Wayland"},
		{name: "gnome", in: GNOME, want: "GNOME Wayland"},
		{name: "unknown", in: Unknown, want: "unknown Wayland"},
		{name: "invalid", in: Session(99), want: "unknown Wayland"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.in.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectRuntimeWithoutWaylandReturnsX11(t *testing.T) {
	t.Parallel()

	rt := env.FromEnviron([]string{"DISPLAY=:1"})

	if got := DetectRuntime(rt); got != X11 {
		t.Fatalf("DetectRuntime = %s, want X11", got)
	}
}

func TestDetectRuntimeUsesWaylandEnvironmentFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  []string
		want Session
	}{
		{
			name: "sway socket",
			env:  []string{"WAYLAND_DISPLAY=missing", "SWAYSOCK=/run/sway.sock"},
			want: Wlroots,
		},
		{
			name: "hyprland signature",
			env:  []string{"WAYLAND_DISPLAY=missing", "HYPRLAND_INSTANCE_SIGNATURE=abc"},
			want: Wlroots,
		},
		{
			name: "kde desktop",
			env:  []string{"WAYLAND_DISPLAY=missing", "XDG_CURRENT_DESKTOP=KDE"},
			want: KDE,
		},
		{
			name: "gnome desktop",
			env:  []string{"WAYLAND_DISPLAY=missing", "XDG_CURRENT_DESKTOP=GNOME"},
			want: GNOME,
		},
		{
			name: "river desktop",
			env:  []string{"WAYLAND_DISPLAY=missing", "XDG_CURRENT_DESKTOP=river"},
			want: Wlroots,
		},
		{
			name: "unknown wayland",
			env:  []string{"WAYLAND_DISPLAY=missing"},
			want: Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt := env.FromEnviron(tt.env)
			if got := DetectRuntime(rt); got != tt.want {
				t.Fatalf("DetectRuntime = %s, want %s", got, tt.want)
			}
		})
	}
}

// A desktop ID is a colon-delimited name, not a free-text substring. Selecting
// a different compositor based on a substring of an unrelated ID can route
// input/window/screen operations to the wrong privileged backend.
func TestDetectRuntimeMatchesDesktopNamesNotSubstringImpostors(t *testing.T) {
	for _, tc := range []struct {
		label   string
		desktop string
		want    Session
	}{
		{"non-KDE vendor", "MYKDE", Unknown},
		{"non-GNOME vendor", "NOTGNOME", Unknown},
		{"non-sway vendor", "NOSWAY", Unknown},
		{"GNOME vendor decorated", "NOTGNOME:BRAND", Unknown},
		{"real GNOME token", "ubuntu:GNOME", GNOME},
		{"real KDE token", "ubuntu:KDE", KDE},
		{"real sway token", "ubuntu:SWAY", Wlroots},
		{"GNOME classic", "GNOME-Classic:GNOME", GNOME},
	} {
		t.Run(tc.label, func(t *testing.T) {
			rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=perfuncted-nonexistent", "XDG_CURRENT_DESKTOP=" + tc.desktop})
			if got := DetectRuntime(rt); got != tc.want {
				t.Fatalf("desktop=%q classified as %s; want %s", tc.desktop, got, tc.want)
			}
		})
	}
}
