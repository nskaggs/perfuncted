package screen

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Release must disconnect the private D-Bus client, not its provider, so a
// discarded screenshot backend cannot continue submitting portal requests.
func TestPortalScreenshotCloseDisconnectsOnlyClient(t *testing.T) {
	var absent *PortalDBusBackend
	if err := absent.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := (&PortalDBusBackend{}).Close(); err != nil {
		t.Fatalf("missing connection close: %v", err)
	}
	backend, provider := newSyntheticScreenshotPortal(t)
	conn := backend.conn
	if conn == nil || !conn.Connected() {
		t.Fatal("private screenshot bus did not connect")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("closing screenshot client: %v", err)
	}
	if conn.Connected() {
		t.Fatal("screenshot connection remained live after close")
	}
	if provider.conn == nil || !provider.conn.Connected() {
		t.Fatal("closing screenshot client disrupted independent provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0)
	if result.Err == nil {
		t.Fatal("closed screenshot client could still query session bus")
	}
	if err := backend.Close(); err != nil && !errors.Is(err, dbus.ErrClosed) {
		t.Fatalf("repeated portal close: %v", err)
	}
}
