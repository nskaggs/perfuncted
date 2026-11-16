package wl

import (
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// A session constructed around a live but uncached Wayland connection must
// own its socket. Closing that session must immediately terminate the private
// compositor peer, even though no cache entry or reference exists to release.
func TestUncachedWaylandSessionClosesItsLiveSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wayland-private.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptUnix()
		if err != nil {
			peerDone <- err
			return
		}
		defer peer.Close()
		_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		var one [1]byte
		_, err = peer.Read(one[:])
		peerDone <- err
	}()
	ctx, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{Sock: socket, Ctx: ctx}
	if err := session.Close(); err != nil {
		t.Fatalf("uncached session close: %v", err)
	}
	if !ctx.closed.Load() {
		t.Fatal("uncached Wayland connection remains open")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("idempotent uncached session close: %v", err)
	}
	select {
	case err := <-peerDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("private compositor not notified of socket close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("private compositor remained blocked after uncached session closed")
	}
}
