package screen

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrResolutionUsesCapturedDimensionsWhenOutputModeIsAbsent(t *testing.T) {
	runtime, err := os.MkdirTemp(os.TempDir(), "wlr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	socket := filepath.Join(runtime, "wayland.sock")
	server, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := server.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- wlrFakeCompositor(conn, "omit output mode")
	}()

	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	width, height, err := backend.Resolution()
	if err != nil {
		t.Fatalf("resolution from captured frame failed: %v", err)
	}
	if width != 2 || height != 2 {
		t.Fatalf("resolution from captured frame = %dx%d, want 2x2", width, height)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic Wayland server: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("compositor did not terminate")
	}
}
