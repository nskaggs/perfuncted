package window

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// Subscribing after shutdown must produce a terminal stream without ever
// dialing the target socket. Even a repeated subscription stays terminal.
func TestSwayWindowChangesAfterCloseNeverDialsAndStaysClosed(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	dials := 0
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("must not dial")
	}
	manager := &SwayManager{sock: "no-target"}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ch := manager.WindowChanges()
		if ch == nil {
			t.Fatal("closed manager returned nil stream")
		}
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("closed manager published event on attempt %d", i)
			}
		case <-time.After(time.Second):
			t.Fatal("closed subscription did not terminate immediately")
		}
	}
	if dials != 0 {
		t.Fatalf("closed manager unexpectedly dialed %d times", dials)
	}
}

// Active admission checks closure before installing the shared query socket.
// Shutdown and a rejected query must never leave the active connection set.
func TestSwayBeginQueryAfterCloseDoesNotInstallConnection(t *testing.T) {
	m := &SwayManager{}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	m.signalClosed()
	if m.beginQuery(client) {
		t.Fatal("closed manager admitted active query")
	}
	if m.activeConn != nil {
		t.Fatal("closed manager retained active query")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
