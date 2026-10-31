package input

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The same private bus connection can have multiple MatchSignal rules: a
// response handler must not treat an unrelated member as permission denial
// or an authoritative grant even when it arrives on the expected request path.
func TestPortalRequestIgnoresUnrelatedSignalMemberOnSameConnection(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	provider.mu.Lock()
	provider.emitUnrelated = true
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	other := []dbus.MatchOption{
		dbus.WithMatchSender(remoteDesktopBus),
		dbus.WithMatchInterface(portalRequestInterface),
		dbus.WithMatchMember("Unexpected"),
	}
	if err := client.AddMatchSignalContext(ctx, other...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), other...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	handle, err := portalCreateSession(ctx, client, name)
	if err != nil || handle != syntheticSessionPath {
		t.Fatalf("unrelated member must not override authorized session: handle=%q error=%v", handle, err)
	}
}
