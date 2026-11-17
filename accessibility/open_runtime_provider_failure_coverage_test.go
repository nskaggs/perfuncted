package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

type failingAccessibilityBusProvider struct{}

func (failingAccessibilityBusProvider) GetAddress() (string, *dbus.Error) {
	return "", dbus.MakeFailedError(errors.New("synthetic address lookup denied"))
}

func TestOpenRuntimeReportsAccessibilityBusProviderFailure(t *testing.T) {
	sessionAddress := startTestDBus(t)
	session, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private session bus: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Export(failingAccessibilityBusProvider{}, busPath, busService); err != nil {
		t.Fatalf("export failing accessibility bus provider: %v", err)
	}
	reply, err := session.RequestName(busService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus service: reply=%v err=%v", reply, err)
	}

	backend, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress,
	}))
	if backend != nil {
		_ = backend.Close()
		t.Fatalf("OpenRuntimeContext returned backend %T after address lookup failed", backend)
	}
	if err == nil || !strings.Contains(err.Error(), "get accessibility bus address") || !strings.Contains(err.Error(), "synthetic address lookup denied") {
		t.Fatalf("OpenRuntimeContext error = %v, want wrapped provider error", err)
	}
}
