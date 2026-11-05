package screen

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

// Probe must distinguish an absent screenshot service from a working, authorized
// GNOME screenshot service on a real isolated session bus.
func TestGnomeShellScreenshotProbePrivateBusAvailability(t *testing.T) {
	_, portal := newSyntheticScreenshotPortal(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress})
	missing := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if missing.Available || !strings.Contains(missing.Reason, "not on session bus") {
		t.Fatalf("missing GNOME service advertised: %+v", missing)
	}
	fake := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register screenshot provider: %v, %v", reply, err)
	}
	if err := portal.conn.Export(fake, gnomeShellShotPath, gnomeShellShotIface); err != nil {
		t.Fatal(err)
	}
	available := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if !available.Available || !strings.Contains(available.Reason, "unsafe mode") {
		t.Fatalf("authorized GNOME service not advertised: %+v", available)
	}
	fake.mu.Lock()
	observed := len(fake.observed)
	fake.deny = true
	fake.mu.Unlock()
	if observed == 0 {
		t.Fatal("probe did not perform an authorization capture")
	}
	denied := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if denied.Available || !strings.Contains(denied.Reason, "permission denied") {
		t.Fatalf("denied GNOME service advertised: %+v", denied)
	}
}
