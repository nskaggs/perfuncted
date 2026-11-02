package screen

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Revocation that arrives during the final pixel consumer must invalidate the
// entire capture transaction. A protocol-ready frame is not permission to
// return its private pixels after the caller has revoked the operation.
func TestWlrScreencopyRejectsCancellationDuringPixelProcessing(t *testing.T) {
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
		serverDone <- wlrFakeCompositor(conn, "")
	}()
	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observedPixels := false
	captureErr := backend.captureFrame(ctx, func(pixels []byte, bi bufInfo) error {
		if bi.width != 2 || bi.height != 2 || len(pixels) != 16 {
			t.Fatalf("synthetic screenshot geometry: %+v bytes=%d", bi, len(pixels))
		}
		observedPixels = true
		cancel()
		return nil
	})
	if !observedPixels {
		t.Fatalf("capture never reached private pixel processing: %v", captureErr)
	}
	if !errors.Is(captureErr, context.Canceled) {
		t.Fatalf("capture reported success after caller revoked pixels: %v", captureErr)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("synthetic compositor not released")
	}
}
