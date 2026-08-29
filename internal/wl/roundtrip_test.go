package wl

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestContextRoundTripHandlesImmediateCallback(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		var request [12]byte
		for {
			if _, readErr := io.ReadFull(conn, request[:]); readErr != nil {
				return
			}
			var event [8]byte
			binary.LittleEndian.PutUint32(event[0:4], binary.LittleEndian.Uint32(request[8:12]))
			binary.LittleEndian.PutUint32(event[4:8], 8<<16)
			if _, writeErr := conn.Write(event[:]); writeErr != nil {
				return
			}
		}
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()

	for i := 0; i < 100; i++ {
		roundTripCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := ctx.RoundTripContext(roundTripCtx)
		cancel()
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i, err)
		}
	}

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Wayland round-trip server did not stop")
	}
}
