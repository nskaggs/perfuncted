//go:build linux

package compositor

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A filtered compositor registry may omit privileged wlroots globals. Only
// explicit compositor session identifiers can authorize the wlroots fallback;
// vendor names containing a token must not be treated as an identity.
func TestWlrootsSessionHintRequiresExplicitIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
		want bool
	}{
		{"sway socket", []string{"SWAYSOCK=/run/user/1000/sway.sock", "XDG_CURRENT_DESKTOP=KDE"}, true},
		{"hyprland instance", []string{"HYPRLAND_INSTANCE_SIGNATURE=session", "XDG_CURRENT_DESKTOP=GNOME"}, true},
		{"nested river", []string{"XDG_CURRENT_DESKTOP=ubuntu:RIVER"}, true},
		{"named wayfire variant", []string{"XDG_CURRENT_DESKTOP=WAYFIRE-Experimental"}, true},
		{"nested hyprland", []string{"XDG_CURRENT_DESKTOP=GNOME:Hyprland"}, true},
		{"vendor spoofed sway", []string{"XDG_CURRENT_DESKTOP=NOTSWAY"}, false},
		{"suffix spoofed hyprland", []string{"XDG_CURRENT_DESKTOP=HyprlandTools"}, false},
		{"generic kde", []string{"XDG_CURRENT_DESKTOP=KDE"}, false},
		{"empty desktop", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := env.FromEnviron(tc.vars)
			if got := wlrootsSessionHint(rt); got != tc.want {
				t.Fatalf("wlroots session hint %v = %v, want %v", tc.vars, got, tc.want)
			}
		})
	}
}
