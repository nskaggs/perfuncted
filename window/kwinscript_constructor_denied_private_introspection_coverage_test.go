//go:build linux

package window

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A service registered under KWin's bus name may deny introspection or
// disappear before a client creates its manager. Neither case grants
// authority to execute desktop automation scripts.
func TestKWinManagerConstructorRejectsDeniedOrRevokedPrivateService(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	if err := provider.conn.Export(deniedKWinIntrospection{}, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewKWinScriptManagerForBusContext(context.Background(), provider.busAddress)
	if manager != nil {
		_ = manager.Close()
		t.Fatal("denied private introspection created KWin manager")
	}
	if err == nil || !strings.Contains(err.Error(), "synthetic private scripting introspection denied") {
		t.Fatalf("introspection failure lost or authorized: %v", err)
	}
	reply, err := provider.conn.ReleaseName(kwinScriptSvc)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("release KWin service: %v %v", reply, err)
	}
	manager, err = NewKWinScriptManagerForBusContext(context.Background(), provider.busAddress)
	if manager != nil {
		_ = manager.Close()
		t.Fatal("revoked KWin service created manager")
	}
	if err == nil || !strings.Contains(err.Error(), "KWin Scripting not on session bus") {
		t.Fatalf("revoked scripting service not rejected: %v", err)
	}
}
