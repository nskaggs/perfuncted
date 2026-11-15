package screen

import (
	"context"
	"image"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A portal can disappear after construction but before capture authorization.
// The original client remains connected to the session bus; it must still
// refuse pixels instead of trusting a stale previously resolved service.
func TestPortalCaptureRejectsRevokedServiceOwnerAfterConstruction(t *testing.T) {
	backend, provider := newSyntheticScreenshotPortal(t)
	if !backend.conn.Connected() {
		t.Fatal("test screenshot session disconnected before revocation")
	}
	reply, err := provider.conn.ReleaseName(portalDest)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("revoke screenshot service: %v %v", reply, err)
	}
	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "resolve screenshot signal owner") {
		t.Fatalf("revoked portal owner allowed capture: img=%v err=%v", img, err)
	}
	if !backend.conn.Connected() {
		t.Fatal("service revocation unexpectedly disconnected client's separate session bus")
	}
}
