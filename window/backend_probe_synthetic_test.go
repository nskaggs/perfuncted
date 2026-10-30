package window

import (
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"strings"
	"testing"
)

func TestSyntheticWindowProbeForeignToplevelPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		globals   map[string]bool
		available bool
		want      string
	}{
		{"no Wayland", nil, false, "no Wayland session"},
		{"no protocols", map[string]bool{}, false, "no foreign-toplevel"},
		{"ext list", map[string]bool{"ext_foreign_toplevel_list_v1": true}, true, "ext_foreign_toplevel_list_v1"},
		{"wlroots control preferred", map[string]bool{"ext_foreign_toplevel_list_v1": true, "zwlr_foreign_toplevel_manager_v1": true}, true, "zwlr_foreign_toplevel_manager_v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := checkForeignToplevel(tc.globals)
			if r.Name != "foreign-toplevel" || r.Available != tc.available || !strings.Contains(r.Reason, tc.want) {
				t.Fatalf("incorrect foreign-toplevel capability %+v", r)
			}
		})
	}
}
func TestSyntheticWindowProbeUnavailableCompositorServices(t *testing.T) {
	absent := env.FromEnviron(nil)
	brokenBus := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-window-bus"})
	for _, tc := range []struct {
		name    string
		rt      env.Runtime
		session compositor.Session
		want    string
	}{
		{"not KDE", absent, compositor.X11, "not a KDE"},
		{"KDE without bus", absent, compositor.KDE, "D-Bus unavailable"},
		{"KDE invalid bus", brokenBus, compositor.KDE, "D-Bus unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkKWinScript(tc.rt, tc.session)
			if r.Available || !strings.Contains(r.Reason, tc.want) {
				t.Fatalf("unexpected KWin probe %+v", r)
			}
		})
	}
	if r := checkGnomeShellEval(absent, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a GNOME") {
		t.Fatalf("X11 GNOME %+v", r)
	}
	if r := checkGnomeShellEval(brokenBus, compositor.GNOME); r.Available || r.Reason == "" {
		t.Fatalf("unavailable GNOME %+v", r)
	}
}
