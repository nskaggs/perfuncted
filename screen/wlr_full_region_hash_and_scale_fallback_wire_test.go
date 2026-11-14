package screen

import (
	"context"
	"image"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Empty region hashes must use the full-frame algorithm, not a special
// zero checksum. An unknown output scale must not multiply a requested
// logical crop into a different physical region.
func TestWlrWireEmptyRegionHashAndUnknownScaleAreConsistent(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wlr.sock")
	server, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, e := server.AcceptUnix()
		if e != nil {
			serverDone <- e
			return
		}
		serverDone <- wlrFakeCompositor(conn, "omit output scale")
	}()
	b := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	defer b.Close()
	whole, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := b.GrabRegionHash(context.Background(), image.Rectangle{})
	if err != nil || empty != whole {
		t.Fatalf("empty-region hash differs from full-frame hash: full=%08x empty=%08x err=%v", whole, empty, err)
	}
	b.scale = 0
	region, err := b.GrabRegionHash(context.Background(), image.Rect(1, 1, 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	want, err := find.PixelHashBGRA(make([]byte, 16), 2, 2, 8, image.Rect(1, 1, 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if region != want {
		t.Fatalf("unknown scale cropped incorrect physical region: got=%08x want=%08x", region, want)
	}
	img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
	if err != nil || img == nil || img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("unknown scale produced invalid logical crop: %v err=%v", img, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("synthetic compositor did not exit")
	}
}
