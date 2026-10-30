package screen

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestSyntheticScreenProbeAdvertisedAndUnavailableProtocols(t *testing.T) {
	for _, tc := range []struct {
		name    string
		globals map[string]bool
		present bool
		why     string
	}{
		{"missing session", nil, false, "no Wayland session"},
		{"empty session", map[string]bool{}, false, "not advertised"},
		{"advertised", map[string]bool{"zwlr_screencopy_manager_v1": true}, true, "advertised"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkWlrScreencopy(tc.globals)
			if r.Name != "wlr-screencopy" || r.Available != tc.present || !strings.Contains(r.Reason, tc.why) {
				t.Fatalf("incorrect capability %+v", r)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		globals map[string]bool
		present bool
	}{
		{"not advertised", map[string]bool{}, false},
		{"no session", nil, false},
		{"capture protocol", map[string]bool{"ext_image_copy_capture_manager_v1": true, "ext_output_image_capture_source_manager_v1": true, "wl_shm": true, "wl_output": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkExtCapture(tc.globals)
			if r.Name != "ext-image-copy-capture" || r.Available != tc.present || r.Reason == "" {
				t.Fatalf("incorrect ext-image capability %+v", r)
			}
		})
	}
}
func TestSyntheticScreenProbeDbusFailureBoundaries(t *testing.T) {
	missing := env.FromEnviron(nil)
	unavailable := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-screen-bus"})
	for _, rt := range []env.Runtime{missing, unavailable} {
		r := checkPortalDbus(rt)
		if r.Available || r.Reason != "D-Bus session unavailable" {
			t.Fatalf("unreachable portal %+v", r)
		}
	}
	if r := checkKWinShot(missing, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a KDE") {
		t.Fatalf("X11 KWin %+v", r)
	}
	if r := checkKWinShot(unavailable, compositor.KDE); r.Available || !strings.Contains(r.Reason, "authorization denied") {
		t.Fatalf("unavailable KWin %+v", r)
	}
	if r := checkGnomeShellScreenshot(missing, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a GNOME") {
		t.Fatalf("X11 GNOME %+v", r)
	}
	if r := checkGnomeShellScreenshot(unavailable, compositor.GNOME); r.Available || r.Reason == "" {
		t.Fatalf("unavailable GNOME %+v", r)
	}
}
