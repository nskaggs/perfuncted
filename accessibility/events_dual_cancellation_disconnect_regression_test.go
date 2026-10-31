package accessibility

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// Both the subscriber-lifetime context and D-Bus connection may be done when
// the dispatcher wakes. Selection order must not determine handle validity.
func TestDisconnectedEventDispatcherInvalidatesHandlesWithBothClosuresReady(t *testing.T) {
	address := startTestDBus(t)
	for attempt := 0; attempt < 48; attempt++ {
		conn, err := dbusutil.ConnectContext(context.Background(), address)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if conn.Context().Err() == nil {
			t.Fatal("connection still live after Close")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := &dbusBackend{generation: 1, access: conn}
		done := make(chan struct{})
		backend.runEventDispatcher(ctx, conn, make(chan *dbus.Signal), nil, nil, nil, done)
		if backend.Generation() != 2 {
			t.Fatalf("iteration %d: bus disconnect lost to canceled dispatcher context; generation=%d", attempt, backend.Generation())
		}
		if !backend.disconnected {
			t.Fatalf("iteration %d: disconnected state was not recorded", attempt)
		}
	}
}
