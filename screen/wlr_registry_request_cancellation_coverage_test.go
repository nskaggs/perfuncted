package screen

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrCaptureCancellationAfterConnectSendsNoRegistryRequest(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverDone <- err
			return
		}
		var request [1]byte
		n, readErr := conn.Read(request[:])
		if n != 0 {
			serverDone <- errors.New("canceled capture sent a Wayland request")
			return
		}
		if !errors.Is(readErr, io.EOF) {
			serverDone <- errors.New("canceled capture did not close its Wayland connection")
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := newWlrScreencopyBackendWithContextConnector(socket, func(_ context.Context, path string) (*wl.Context, error) {
		conn, connectErr := wl.ConnectContext(context.Background(), path)
		if connectErr != nil {
			return nil, connectErr
		}
		// The socket is acquired, but protocol initialization must still honor
		// cancellation before it publishes or sends any registry request.
		cancel()
		return conn, nil
	}, time.Minute)
	t.Cleanup(func() { _ = backend.Close() })

	hash, captureErr := backend.GrabFullHash(ctx)
	if hash != 0 || !errors.Is(captureErr, context.Canceled) {
		t.Fatalf("canceled registry setup returned hash=%08x err=%v", hash, captureErr)
	}
	if backend.ctx != nil {
		t.Fatal("backend retained the connection after canceled registry setup")
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic compositor did not observe connection cleanup")
	}
}
