package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestOpenRuntimeUnknownCompositorReturnsCancellationDuringExtCaptureSetup(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		for index := 0; index < 3; index++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			if index < 2 {
				if serveErr := serveExtConstructorPeerWithGlobals(conn, "", nil); serveErr != nil {
					serverDone <- serveErr
					return
				}
				continue
			}
			serverDone <- cancelExtCaptureRegistryRoundTrip(conn, cancel)
			return
		}
	}()

	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=UnrecognizedSyntheticDesktop",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("canceled ext-capture setup returned a screenshot backend")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime error = %v, want context.Canceled", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ext-capture setup did not close its canceled registry connection")
	}
}

func cancelExtCaptureRegistryRoundTrip(conn *net.UnixConn, cancel context.CancelFunc) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return fmt.Errorf("read ext-capture setup request: %w", err)
		}
		size := int(wl.Uint32(header[4:]) >> 16)
		if size < 8 || size > 16384 {
			return fmt.Errorf("invalid ext-capture setup request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return fmt.Errorf("read ext-capture setup request body: %w", err)
		}
		if wl.Uint32(header[:4]) == 1 && wl.Uint32(header[4:])&0xffff == 0 {
			cancel()
			var unexpected [1]byte
			if _, err := conn.Read(unexpected[:]); !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("canceled client did not close its registry connection: %w", err)
			}
			return nil
		}
	}
}
