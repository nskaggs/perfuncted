package input

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPortalSubscribeClosedCleansSignalChannelWhenMatchIsRejected(t *testing.T) {
	client, _ := startSyntheticPortalBus(t)
	portal := &portalSession{
		conn: client,
		path: dbus.ObjectPath("/invalid object path"),
	}

	if err := portal.subscribeClosed(context.Background()); err == nil {
		t.Fatal("D-Bus accepted an invalid session path match")
	}
	if portal.owner == "" || len(portal.match) != 4 {
		t.Fatalf("invalid match did not pass owner resolution and rule construction: owner=%q rules=%d", portal.owner, len(portal.match))
	}
	if portal.signals != nil {
		t.Fatal("failed D-Bus match left a registered signal channel")
	}
}
