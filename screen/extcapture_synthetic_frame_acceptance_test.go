package screen

import (
	"context"
	"fmt"
	"github.com/nskaggs/perfuncted/find"
	"image"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A synthetic Wayland socket advertises actual session constraints and frame
// readiness, while the backend performs the real shm/pool/frame wire sequence.
func TestExtCaptureSyntheticWaylandCaptureLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation func(*ExtCaptureBackend) (image.Image, uint32, error)
		wantImage bool
	}{
		{"full image", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, true},
		{"cropped image", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
			return img, 0, err
		}, true},
		{"full hash", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}, false},
		{"region hash", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return nil, h, err
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			serverTrace := make(chan string, 64)
			releaseServer := make(chan struct{})
			go func() {
				conn, acceptErr := listener.AcceptUnix()
				if acceptErr != nil {
					serverDone <- acceptErr
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				request := make([]byte, 12)
				if _, lintShadow61 := io.ReadFull(conn, request); lintShadow61 != nil {
					serverDone <- lintShadow61
					return
				}
				callbackID := wl.Uint32(request[8:12])
				serverTrace <- fmt.Sprintf("initial display sync callback=%d", callbackID)
				// The test registers wl_shm as object 2 and session as object 3.
				for _, event := range []struct {
					sender, op uint32
					body       []byte
				}{
					{3, 0, wlUint32Payload(2, 2)}, {3, 1, wlUint32Payload(0)}, {callbackID, 0, nil},
				} {
					if lintShadow74 := writeExtTestEvent(conn, event.sender, event.op, event.body); lintShadow74 != nil {
						serverDone <- lintShadow74
						return
					}
				}
				var frameID uint32
				for {
					var header [8]byte
					if _, lintShadow82 := io.ReadFull(conn, header[:]); lintShadow82 != nil {
						serverDone <- lintShadow82
						return
					}
					size := int(wl.Uint32(header[4:8])>>16) - 8
					if size < 0 || size > 4096 {
						serverDone <- io.ErrUnexpectedEOF
						return
					}
					body := make([]byte, size)
					if _, lintShadow92 := io.ReadFull(conn, body); lintShadow92 != nil {
						serverDone <- lintShadow92
						return
					}
					sender := wl.Uint32(header[:4])
					opcode := wl.Uint32(header[4:8]) & 0xffff
					serverTrace <- fmt.Sprintf("request sender=%d opcode=%d size=%d", sender, opcode, size)
					if sender == 3 && opcode == 0 && len(body) >= 4 {
						frameID = wl.Uint32(body[:4])
					}
					if frameID != 0 && sender == frameID && opcode == 3 {
						lintShadow103 := writeExtTestEvent(conn, frameID, 3, nil)
						serverDone <- lintShadow103
						<-releaseServer
						return
					}
				}
			}()
			ctx, err := wl.Connect(sock)
			if err != nil {
				close(releaseServer)
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			ctx.Register(shm)
			ctx.Register(sess)
			if shm.ID() != 2 || sess.ID() != 3 {
				close(releaseServer)
				t.Fatalf("unexpected fixture objects: shm=%d sess=%d", shm.ID(), sess.ID())
			}
			backend := &ExtCaptureBackend{session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)}, shm: shm, sessProxy: sess, outputScale: 1}
			done := make(chan struct{})
			var img image.Image
			var hash uint32
			var captureErr error
			go func() { img, hash, captureErr = tc.operation(backend); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				var trace []string
				for len(serverTrace) > 0 {
					trace = append(trace, <-serverTrace)
				}
				t.Fatalf("capture did not complete; trace=%v", trace)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("compositor never saw frame capture")
			}
			close(releaseServer)
			if captureErr != nil {
				t.Fatalf("screencopy failed: %v", captureErr)
			}
			if tc.wantImage {
				if img == nil {
					t.Fatal("screencopy returned nil")
				}
				if tc.name == "cropped image" && img.Bounds().Size() != image.Pt(1, 1) {
					t.Fatalf("cropped image wrong geometry: %v", img.Bounds())
				}
				if tc.name == "full image" && img.Bounds().Size() != image.Pt(2, 2) {
					t.Fatalf("full image wrong geometry: %v", img.Bounds())
				}
			} else {
				rect := image.Rectangle{}
				if tc.name == "region hash" {
					rect = image.Rect(0, 0, 1, 1)
				}
				want, err := find.PixelHashBGRA(make([]byte, 16), 2, 2, 8, rect)
				if err != nil {
					t.Fatal(err)
				}
				if hash != want {
					t.Fatalf("framebuffer hash=%08x, want %08x", hash, want)
				}
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}
