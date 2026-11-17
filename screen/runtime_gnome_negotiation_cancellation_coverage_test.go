package screen

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type cancelGnomeScreenNegotiation struct {
	cancel  context.CancelFunc
	started chan struct{}
	release chan struct{}
}

func (s *cancelGnomeScreenNegotiation) GetProtocolVersion() (uint32, *dbus.Error) {
	close(s.started)
	s.cancel()
	<-s.release
	return gnomebridge.ProtocolVersion, nil
}

func TestScreenRuntimeCancellationStopsGNOMEFallbackAfterBridgeNegotiation(t *testing.T) {
	_, bus := newSyntheticScreenshotPortal(t)
	reply, err := bus.conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &cancelGnomeScreenNegotiation{
		cancel:  cancel,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(service.release)
	if err := bus.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export canceling GNOME bridge core: %v", err)
	}
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + bus.busAddress,
	})

	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME runtime selected %T with error %v", backend, err)
	}
	select {
	case <-service.started:
	default:
		t.Fatal("GNOME bridge negotiation did not trigger cancellation")
	}
}
