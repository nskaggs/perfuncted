//go:build linux

package compositor

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Cancellation while the real Wayland registry is unresponsive must win over
// stale desktop authority; the probe must close its socket and return promptly.
func TestCompositorCancellationDuringRegistryProbeFailsClosed(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "display.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	connected := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		close(connected)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var b [1]byte
		for {
			if _, err := conn.Read(b[:]); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=display.sock", "XDG_CURRENT_DESKTOP=KDE"})
	result := make(chan struct {
		kind Session
		err  error
	}, 1)
	go func() {
		kind, err := DetectRuntimeContext(ctx, rt)
		result <- struct {
			kind Session
			err  error
		}{kind, err}
	}()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("Wayland registry probe did not connect")
	}
	cancel()
	select {
	case got := <-result:
		if got.kind != Unknown || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled registry probe selected %v with err %v", got.kind, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled registry probe did not return")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled registry probe did not close socket")
	}
}
