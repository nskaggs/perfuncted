package wl

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestListGlobalsBoundsWedgedSocket verifies that env-detection helpers give
// up on a compositor socket that accepts connections but never replies,
// instead of blocking forever. This pins the round-trip budget that keeps
// Open responsive to wedged compositors.
func TestListGlobalsBoundsWedgedSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "wedged-wayland-0")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept connections but never speak the Wayland protocol: exactly what
	// a frozen or SIGSTOPed compositor looks like from the client side.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				conn.Close()
			}()
		}
	}()

	if !SocketReachable(sock) {
		t.Fatal("test setup: socket should be reachable")
	}

	old := defaultRoundTripTimeout
	defaultRoundTripTimeout = 250 * time.Millisecond
	defer func() { defaultRoundTripTimeout = old }()

	finished := make(chan bool, 1)
	go func() {
		s, err := NewSession(sock)
		if err == nil {
			s.Close()
		}
		finished <- true
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		close(done) // unblock helper goroutines before failing hard
		t.Fatal("NewSession against a wedged socket did not return within the round-trip budget")
	}

	// The public detection helper must also stay bounded.
	globalsDone := make(chan bool, 1)
	go func() {
		ListGlobals(sock)
		globalsDone <- true
	}()
	select {
	case <-globalsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ListGlobals against a wedged socket did not return within the round-trip budget")
	}

	_ = os.Remove(sock)
}
