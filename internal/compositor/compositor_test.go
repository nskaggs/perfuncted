package compositor

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

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
