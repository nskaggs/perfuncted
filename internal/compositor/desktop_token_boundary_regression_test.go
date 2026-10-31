package compositor

import (
	"github.com/nskaggs/perfuncted/internal/env"
	"testing"
)

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
