package wl

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An untrusted compositor can disconnect at either stage of the session
// handshake. Neither failure may publish a cached session or live transport.
func TestSessionHandshakeRejectsDisconnectedCompositorWithoutCaching(t *testing.T) {
	for _, phase := range []string{"after-registry-request", "after-sync-request"} {
		t.Run(phase, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				peer, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer peer.Close()
				var header [8]byte
				if _, err = io.ReadFull(peer, header[:]); err != nil {
					done <- err
					return
				}
				if phase == "after-registry-request" {
					done <- nil
					return
				}
				var registryRequest [4]byte
				if _, err = io.ReadFull(peer, registryRequest[:]); err != nil {
					done <- err
					return
				}
				if _, err = io.ReadFull(peer, header[:]); err != nil {
					done <- err
					return
				}
				var syncRequest [4]byte
				if _, err = io.ReadFull(peer, syncRequest[:]); err != nil {
					done <- err
					return
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			session, openErr := NewSessionContext(ctx, socket)
			if session != nil {
				_ = session.Close()
				t.Fatal("disconnected compositor produced live session")
			}
			if openErr == nil {
				t.Fatal("disconnected compositor was accepted")
			}
			if phase == "after-registry-request" && !strings.Contains(openErr.Error(), "registry round-trip") {
				t.Fatalf("missing registry round-trip failure detail: %v", openErr)
			}
			if phase == "after-sync-request" && !strings.Contains(openErr.Error(), "registry round-trip") {
				t.Fatalf("missing sync failure detail: %v", openErr)
			}
			if err := <-done; err != nil {
				t.Fatalf("synthetic compositor failed: %v", err)
			}
			sessionCacheMu.Lock()
			_, cached := sessionCache[socket]
			sessionCacheMu.Unlock()
			if cached {
				t.Fatal("failed Wayland handshake published cached session")
			}
		})
	}
}

// Missing or zero-value display contexts cannot be used for a registry
// round-trip, and a zero-value transport cannot block on a network operation.
func TestUnconnectedWaylandProtocolDispatchAndDisplayRoundTripAreInert(t *testing.T) {
	var transport Context
	if err := transport.dispatch(); err != nil {
		t.Fatalf("unconnected dispatch: %v", err)
	}
	var display *Display
	if err := display.RoundTrip(); err != nil {
		t.Fatalf("nil display round trip: %v", err)
	}
	if err := (&Display{}).RoundTrip(); err != nil {
		t.Fatalf("unconnected display round trip: %v", err)
	}
	if _, err := ConnectContext(context.Background(), "/this/wayland/socket/does/not/exist"); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("missing compositor returned unexpected connection result: %v", err)
	}
}
