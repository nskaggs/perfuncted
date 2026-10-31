package input

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// The session owner is authenticated at subscription. If lifecycle state is
// malformed or partially initialized, an unrelated peer must not be able to
// forge a session-close event by knowing the predictable portal object path.
func TestRemoteDesktopClosedSignalWithoutAuthenticatedOwnerFailsClosed(t *testing.T) {
	if (&RemoteDesktopBackend{}).isAuthorizedPortalSessionClose(&dbus.Signal{Name: portalSessionInterface + ".Closed"}) {
		t.Fatal("uninitialized portal accepted lifecycle event")
	}
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/session/authorized")
	backend := &RemoteDesktopBackend{portal: &portalSession{path: path}}
	for _, sender := range []string{"", ":1.44", ":1.66"} {
		forged := &dbus.Signal{Sender: sender, Path: path, Name: portalSessionInterface + ".Closed"}
		if backend.isAuthorizedPortalSessionClose(forged) {
			t.Fatalf("accepted lifecycle event from %q without authenticated portal owner", sender)
		}
	}
}
