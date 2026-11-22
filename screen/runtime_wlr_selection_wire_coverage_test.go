package screen

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeSelectsWlrScreencopyFromAdvertisedManager(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- fmt.Errorf("accept Wayland registry connection %d: %w", i+1, acceptErr)
				return
			}
			globals := []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 3}}
			if serveErr := serveExtConstructorPeerWithGlobals(conn, "", globals); serveErr != nil {
				serverDone <- fmt.Errorf("serve Wayland registry connection %d: %w", i+1, serveErr)
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=Other",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open WLR runtime with advertised screencopy manager: %v", err)
	}
	wlr, ok := backend.(*WlrScreencopyBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("runtime selected %T, want WlrScreencopyBackend", backend)
	}
	if !wlr.CanonicalHashing() {
		t.Fatal("selected WLR backend did not advertise canonical hashes")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close selected WLR backend: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("runtime did not close both registry connections")
	}
}

func TestOpenRuntimeUsesWlrProtocolForUnknownCompositor(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		for index := 0; index < 2; index++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- fmt.Errorf("accept Wayland connection %d: %w", index+1, acceptErr)
				return
			}
			var globals []extConstructorAdvertisedGlobal
			if index == 1 {
				globals = []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 3}}
			}
			if serveErr := serveExtConstructorPeerWithGlobals(conn, "", globals); serveErr != nil {
				serverDone <- fmt.Errorf("serve Wayland connection %d: %w", index+1, serveErr)
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=UnrecognizedSyntheticDesktop",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open WLR runtime after unknown compositor probe: %v", err)
	}
	wlr, ok := backend.(*WlrScreencopyBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("runtime selected %T, want WlrScreencopyBackend", backend)
	}
	if !wlr.CanonicalHashing() {
		t.Fatal("selected WLR backend did not advertise canonical hashes")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close selected WLR backend: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("runtime did not complete protocol discovery and setup")
	}
}
