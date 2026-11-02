package screen

import (
	"context"
	"image"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A compositor's frame.ready event arriving before the capture copy request
// cannot authorize pixels. Otherwise a producer can skip the copy entirely
// and the consumer would publish an uninitialized shared-memory screenshot.
func TestWlrScreencopyRejectsReadyBeforeCopyProtocolViolation(t *testing.T) {
	for _, outcome := range []string{"ready before buffer", "premature ready"} {
		t.Run(outcome, func(t *testing.T) {
			testWlrReadyBeforeCopyFailsClosed(t, outcome)
		})
	}
}

func testWlrReadyBeforeCopyFailsClosed(t *testing.T, outcome string) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "wayland.sock")
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
		serverDone <- wlrFakeCompositor(conn, outcome)
	}()
	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if img != nil || err == nil {
		t.Errorf("pre-copy ready was accepted as a completed screenshot: img=%v err=%v", img, err)
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
		t.Fatal("synthetic compositor leaked")
	}
}
