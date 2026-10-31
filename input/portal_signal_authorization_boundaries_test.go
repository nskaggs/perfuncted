//go:build linux

package input

import (
	"github.com/godbus/dbus/v5"
	"testing"
)

func TestRemoteDesktopPortalClosedSignalRequiresSessionPathAndOwner(t *testing.T) {
	const owner = ":1.44"
	const path = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/authorized")
	backend := &RemoteDesktopBackend{portal: &portalSession{owner: owner, path: path}}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
		want   bool
	}{
		{"nil", nil, false},
		{"other interface", &dbus.Signal{Name: "org.freedesktop.portal.Request.Closed", Path: path, Sender: owner}, false},
		{"other member", &dbus.Signal{Name: portalSessionInterface + ".Opened", Path: path, Sender: owner}, false},
		{"other session", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: "/org/freedesktop/portal/desktop/session/other", Sender: owner}, false},
		{"forged owner", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: path, Sender: ":1.66"}, false},
		{"authentic owner", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: path, Sender: owner}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backend.isAuthorizedPortalSessionClose(tc.signal); got != tc.want {
				t.Fatalf("signal %+v accepted=%v want %v", tc.signal, got, tc.want)
			}
		})
	}
}
