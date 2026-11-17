package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimePropagatesCancellationDuringCompositorDetection(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var connections atomic.Int32
	probeConnected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept compositor probe: %w", acceptErr)
			return
		}
		connections.Add(1)
		close(probeConnected)
		defer conn.Close()
		if deadlineErr := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			serverDone <- fmt.Errorf("bound compositor probe observation: %w", deadlineErr)
			return
		}
		if _, readErr := io.Copy(io.Discard, conn); readErr != nil {
			serverDone <- fmt.Errorf("observe canceled compositor probe: %w", readErr)
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=Sway",
	})
	type result struct {
		backend Screenshotter
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		backend, openErr := OpenRuntimeContext(ctx, runtime)
		opened <- result{backend: backend, err: openErr}
	}()

	select {
	case <-probeConnected:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not connect for compositor detection")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled compositor detection = (%T, %v), want (nil, context.Canceled)", got.backend, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime selection did not return after compositor detection cancellation")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic Wayland peer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled compositor probe did not close its connection")
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("Wayland connection attempts = %d, want compositor detection only", got)
	}
}
