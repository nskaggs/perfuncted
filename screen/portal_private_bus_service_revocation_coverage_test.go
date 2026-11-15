package screen

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A reachable D-Bus session does not imply that the screenshot portal service
// is still present. When its name is relinquished, new capture backends must
// fail closed rather than advertising a screenshot capability.
func TestPortalConstructorRejectsRevokedPrivateScreenshotService(t *testing.T) {
	_, provider := newSyntheticScreenshotPortal(t)
	reply, err := provider.conn.ReleaseName(portalDest)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("release synthetic portal service: %v %v", reply, err)
	}
	backend, err := NewPortalDBusBackendForBusContext(context.Background(), provider.busAddress)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("absent screenshot portal was authorized")
	}
	if err == nil || !strings.Contains(err.Error(), "not on session bus") {
		t.Fatalf("missing portal not rejected: %v", err)
	}
}
