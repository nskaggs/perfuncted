package screen

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A portal-supplied URI cannot use non-hexadecimal percent encodings to
// smuggle an attacker-controlled local screenshot path through decoding.
func TestPortalScreenshotRejectsNonHexPercentEscapesAndPreservesValidEscapes(t *testing.T) {
	for _, uri := range []string{
		"file:///var/tmp/screenshot%GG.png",
		"file:///var/tmp/screenshot%2G.png",
		"file:///var/tmp/screenshot%G2.png",
		"file:///var/tmp/screenshot%zz.png",
	} {
		t.Run(uri, func(t *testing.T) {
			path, err := fileURIPath(uri)
			if path != "" || err == nil || !strings.Contains(err.Error(), "invalid escape") {
				t.Fatalf("invalid encoded path accepted: path=%q err=%v", path, err)
			}
		})
	}
	path, err := fileURIPath("file:///var/tmp/screenshot%25literal.png")
	if err != nil || path != "/var/tmp/screenshot%literal.png" {
		t.Fatalf("valid literal percent escape rejected: path=%q err=%v", path, err)
	}
}

// Well-known bus names are not authentication identities. The portal request
// handle must be derived from a private unique name, never a service alias.
func TestPortalScreenshotRejectsMissingUniqueBusNameAndNullSignals(t *testing.T) {
	if name, err := portalUniqueName([]string{"org.freedesktop.portal.Desktop", "org.example.Application"}); name != "" || err == nil || !strings.Contains(err.Error(), "no unique") {
		t.Fatalf("service alias used as request identity: name=%q err=%v", name, err)
	}
	trusted := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_44/pf123")
	if portalSignalMatches(nil, trusted) {
		t.Fatal("null response signal matched a trusted request path")
	}
	if portalSignalMatches(&dbus.Signal{Path: trusted}, dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_44/pf456")) {
		t.Fatal("unrelated response handle matched authenticated request")
	}
	if !portalSignalMatches(&dbus.Signal{Path: trusted}, dbus.ObjectPath(""), trusted) {
		t.Fatal("valid returned handle not accepted after empty optional handle")
	}
}
