package screen

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestFileURIPath(t *testing.T) {
	path, err := fileURIPath("file:///tmp/portal%20shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/portal shot.png" {
		t.Fatalf("path = %q", path)
	}
}

func TestFileURIPathRejectsUnsupportedHost(t *testing.T) {
	if _, err := fileURIPath("file://remotehost/tmp/portal.png"); err == nil {
		t.Fatal("expected error for unsupported host")
	}
}

func TestPortalUniqueNamePrefersUniqueBusName(t *testing.T) {
	got, err := portalUniqueName([]string{"org.freedesktop.portal.Desktop", ":1.198", "org.example.App"})
	if err != nil {
		t.Fatalf("portalUniqueName: %v", err)
	}
	if got != ":1.198" {
		t.Fatalf("portalUniqueName = %q, want %q", got, ":1.198")
	}
}

func TestPortalRequestPath(t *testing.T) {
	got := portalRequestPath(":1.198", "pf123")
	want := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	if got != want {
		t.Fatalf("portalRequestPath = %q, want %q", got, want)
	}
}

func TestPortalSignalMatchesReturnedHandle(t *testing.T) {
	expected := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	returned := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf999")
	sig := &dbus.Signal{Path: returned}
	if !portalSignalMatches(sig, expected, returned) {
		t.Fatal("portalSignalMatches should accept the returned handle path")
	}
	if portalSignalMatches(sig, expected) {
		t.Fatal("portalSignalMatches unexpectedly matched the wrong expected path")
	}
}
