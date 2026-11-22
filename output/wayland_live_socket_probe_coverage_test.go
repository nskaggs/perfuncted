package output

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// A reachable Wayland socket takes precedence over an unrelated stale X11
// DISPLAY. A private compositor provides a genuine registry round-trip.
func TestProbeRuntimeSelectsLiveWaylandDespiteX11Fallback(t *testing.T) {
	dir := shortPrivateSocketDir(t)
	socket := filepath.Join(dir, "wayland.sock")
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
		defer conn.Close()
		var registry uint32
		for {
			request, readErr := readWaylandMessage(conn)
			if readErr != nil {
				done <- readErr
				return
			}
			sender, opcode := wl.Uint32(request[:4]), wl.Uint32(request[4:8])&0xffff
			payload := request[8:]
			if sender != 1 {
				continue
			}
			switch opcode {
			case 1:
				registry = wl.Uint32(payload[:4])
			case 0:
				if err := sendWaylandGlobalEvents(conn, registry); err != nil {
					done <- err
					return
				}
				if err := writeWaylandMessage(conn, wl.Uint32(payload[:4]), 0, nil); err != nil {
					done <- err
					return
				}
				done <- nil
				return
			}
		}
	}()
	rt := env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=wayland.sock", "DISPLAY=invalid-display-name", "XDG_CURRENT_DESKTOP=SWAY"})
	got := ProbeRuntime(rt)
	if len(got) != 1 || got[0].Name != "wayland" || !got[0].Available || !got[0].Selected || got[0].Reason != "wlroots Wayland" {
		t.Fatalf("reachable Wayland probe selected fallback: %+v", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("synthetic compositor failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic compositor did not finish registry reply")
	}
}
