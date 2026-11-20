package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeStopsFallbackWhenWlrootsSetupIsCanceled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		desktop string
	}{
		{name: "explicit wlroots compositor", desktop: "Sway"},
		{name: "unknown Wayland compositor", desktop: "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeStopsFallbackWhenPrimaryWaylandSetupIsCanceled(t, tc.desktop)
		})
	}
}

func assertRuntimeStopsFallbackWhenPrimaryWaylandSetupIsCanceled(t *testing.T, desktop string) {
	t.Helper()
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var connections atomic.Int32
	secondConnected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		probe, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept compositor probe: %w", acceptErr)
			return
		}
		connections.Add(1)
		if serveErr := serveExtConstructorPeer(probe, ""); serveErr != nil {
			serverDone <- fmt.Errorf("serve compositor probe: %w", serveErr)
			return
		}

		setup, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept wlroots backend setup: %w", acceptErr)
			return
		}
		connections.Add(1)
		close(secondConnected)
		defer setup.Close()
		if deadlineErr := setup.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			serverDone <- fmt.Errorf("bound backend setup observation: %w", deadlineErr)
			return
		}
		if _, readErr := io.Copy(io.Discard, setup); readErr != nil {
			serverDone <- fmt.Errorf("observe canceled wlroots setup: %w", readErr)
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=" + desktop,
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
	case <-secondConnected:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not start wlroots backend setup after compositor detection")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled backend selection = (%T, %v), want (nil, context.Canceled)", got.backend, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime selection did not return after backend setup cancellation")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled Wayland setup did not close its connection")
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("Wayland connection attempts = %d, want detection plus canceled primary backend only", got)
	}
}
