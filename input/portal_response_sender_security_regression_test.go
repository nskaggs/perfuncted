package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// A bus peer must not authorize the caller by fabricating a Request.Response
// at the portal's returned object path. Although normal sessions use narrow
// match rules, godbus dispatches the union of all rules to each signal channel.
func TestPortalRequestRejectsSamePathResponseFromUntrustedBusSender(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	pathReady := make(chan dbus.ObjectPath, 1)
	portal.mu.Lock()
	portal.skip = true
	portal.requestPaths = pathReady
	portal.mu.Unlock()
	attacker, err := dbusutil.ConnectContext(context.Background(), portal.busAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	wideRule := []dbus.MatchOption{
		dbus.WithMatchInterface(portalRequestInterface), dbus.WithMatchMember("Response"),
	}
	if err := client.AddMatchSignalContext(ctx, wideRule...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), wideRule...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := portalCreateSession(ctx, client, name); done <- err }()
	var path dbus.ObjectPath
	select {
	case path = <-pathReady:
	case <-ctx.Done():
		t.Fatalf("portal method not dispatched: %v", ctx.Err())
	}
	forged := map[string]dbus.Variant{"session_handle": dbus.MakeVariant(syntheticSessionPath)}
	if err := attacker.Emit(path, portalRequestInterface+".Response", uint32(0), forged); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("untrusted peer forged a successful RemoteDesktop authorization")
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("untrusted response leaked unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("forged response caused portal request to hang")
	}
}
