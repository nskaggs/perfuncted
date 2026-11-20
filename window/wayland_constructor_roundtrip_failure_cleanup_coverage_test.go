//go:build linux

package window

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func serveWindowCompositorWithMalformedManagerRoundTrip(conn *net.UnixConn) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registryID, managerID uint32
	syncs := 0
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return err
		}
		size := int(wl.Uint32(header[4:8]) >> 16)
		if size < len(header) || size > 65535 {
			return fmt.Errorf("invalid request size %d", size)
		}
		body := make([]byte, size-len(header))
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff
		switch {
		case sender == 1 && opcode == 1:
			registryID = wl.Uint32(body)
		case sender == registryID && opcode == 0:
			if len(body) < 12 || wl.Uint32(body[:4]) != 41 {
				return fmt.Errorf("unexpected registry bind request %x", body)
			}
			managerID = wl.Uint32(body[len(body)-4:])
		case sender == 1 && opcode == 0:
			syncs++
			callbackID := wl.Uint32(body)
			if syncs == 1 {
				if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(41, "zwlr_foreign_toplevel_manager_v1", 3)); err != nil {
					return err
				}
				if err := wireWindowEvent(conn, callbackID, 0, wireWindowWords(0)); err != nil {
					return err
				}
				continue
			}
			if syncs != 2 || managerID == 0 {
				return fmt.Errorf("unexpected setup round trip %d (manager %d)", syncs, managerID)
			}
			// A malformed server frame makes setup fail. The constructor must
			// close the session it opened before returning the error.
			var malformed [8]byte
			wl.PutUint32(malformed[:4], 1)
			wl.PutUint32(malformed[4:], 4<<16)
			n, err := conn.Write(malformed[:])
			if err != nil {
				return fmt.Errorf("send malformed frame: %w", err)
			}
			if n != len(malformed) {
				return fmt.Errorf("send malformed frame: wrote %d bytes", n)
			}
			var probe [1]byte
			if _, err := conn.Read(probe[:]); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return fmt.Errorf("wait for constructor to close failed session: %w", err)
			}
			return errors.New("constructor left the failed Wayland session open")
		}
	}
}

func TestWaylandConstructorClosesSessionAfterManagerRoundTripFailure(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		done <- serveWindowCompositorWithMalformedManagerRoundTrip(conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	manager, err := NewWaylandWindowManagerForSocketContext(ctx, socket)
	if manager != nil || err == nil || !strings.Contains(err.Error(), "invalid message size") {
		t.Fatalf("constructor returned manager=%v err=%v, want failed setup round trip", manager, err)
	}
	select {
	case serverErr := <-done:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatal("constructor did not close the failed Wayland session")
	}
}
