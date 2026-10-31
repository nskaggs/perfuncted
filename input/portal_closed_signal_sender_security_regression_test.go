package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// An unrelated peer can emit a Closed signal with the exact portal session
// object path. D-Bus match rules are delivered to a shared signal channel,
// so matching path and member alone must not revoke a granted session.
func TestRemoteDesktopPortalClosedSignalRejectsUntrustedBusSender(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	attacker, err := dbusutil.ConnectContext(context.Background(), provider.busAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := &portalSession{conn: client, path: dbus.ObjectPath(syntheticSessionPath), resourcesDone: make(chan struct{})}
	if err := p.subscribeClosed(ctx); err != nil {
		t.Fatal(err)
	}
	// Introduce a second broad subscriber, as occurs when multiple request
	// handlers share this godbus connection.
	broad := []dbus.MatchOption{dbus.WithMatchInterface(portalSessionInterface), dbus.WithMatchMember("Closed")}
	if err := client.AddMatchSignalContext(ctx, broad...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), broad...) }()
	connection, peer := unixConnectionPair(t)
	defer peer.Close()
	eis := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	sender := &eiSender{client: eis, connection: 0xff00000000000010, seat: 0xff00000000000011, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: 0xff00000000000012, buttonDev: 0xff00000000000012, scrollDev: 0xff00000000000012, textDev: 0xff00000000000012}
	b := newRemoteDesktopBackend(sender, p)
	defer func() { _ = b.Close() }()
	if err := attacker.Emit(p.path, portalSessionInterface+".Closed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.revoked:
		t.Fatal("an unrelated D-Bus peer revoked live portal authorization")
	case <-time.After(45 * time.Millisecond):
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("forged closure changed healthy session state: %v", err)
	}
	if err := provider.conn.Emit(p.path, portalSessionInterface+".Closed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("authoritative portal session closure was ignored")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("provider closure not recognized: %v", err)
	}
}
