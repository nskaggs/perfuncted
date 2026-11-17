package screen

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestExtCaptureConstructorAppliesAdvertisedOutputScaleEvent(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			server <- err
			return
		}
		server <- serveExtConstructorPeerWithOutputScale(conn, "", nil, 2)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := NewExtCaptureBackendForSocketContext(ctx, socket)
	if err != nil {
		t.Fatalf("construct ext capture backend: %v", err)
	}
	if backend.outputScale != 0 {
		t.Fatalf("output scale updated before dispatch: %d", backend.outputScale)
	}
	if err := backend.session.Display.RoundTripContext(ctx); err != nil {
		_ = backend.Close()
		t.Fatalf("dispatch compositor output scale: %v", err)
	}
	if backend.outputScale != 2 {
		t.Fatalf("compositor output scale = %d, want 2", backend.outputScale)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend: %v", err)
	}
	select {
	case err := <-server:
		if err != nil {
			t.Fatalf("synthetic compositor rejected wire requests: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backend failed to release compositor connection")
	}
}
