package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureRealWaylandFailedFrameNeverReturnsImageOrHash(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(context.Context, *ExtCaptureBackend) error
	}{
		{"image", func(ctx context.Context, b *ExtCaptureBackend) error {
			img, err := b.Grab(ctx, image.Rectangle{})
			if img != nil {
				return fmt.Errorf("frame failure returned image %v", img)
			}
			return err
		}},
		{"full hash", func(ctx context.Context, b *ExtCaptureBackend) error {
			h, err := b.GrabFullHash(ctx)
			if h != 0 {
				return fmt.Errorf("frame failure returned hash %d", h)
			}
			return err
		}},
		{"region hash", func(ctx context.Context, b *ExtCaptureBackend) error {
			h, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 1, 1))
			if h != 0 {
				return fmt.Errorf("frame failure returned region hash %d", h)
			}
			return err
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			sock := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				first := make([]byte, 12)
				if _, err := io.ReadFull(conn, first); err != nil {
					serverDone <- err
					return
				}
				callback := wl.Uint32(first[8:])
				for _, event := range []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(2, 2)}, {3, 1, wlUint32Payload(0)}, {callback, 0, nil},
				} {
					if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
						serverDone <- err
						return
					}
				}
				var frameID uint32
				sawCapture := false
				for {
					var h [8]byte
					if _, err := io.ReadFull(conn, h[:]); err != nil {
						if errors.Is(err, io.EOF) && sawCapture {
							serverDone <- nil
						} else {
							serverDone <- fmt.Errorf("post-capture EOF=%v captured=%v", err, sawCapture)
						}
						return
					}
					size := int(wl.Uint32(h[4:]) >> 16)
					if size < 8 || size > 8192 {
						serverDone <- fmt.Errorf("malformed frame request size %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, err := io.ReadFull(conn, body); err != nil {
						serverDone <- err
						return
					}
					sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:])&0xffff
					if sender == 3 && opcode == 0 && len(body) >= 4 {
						frameID = wl.Uint32(body)
					}
					if frameID != 0 && sender == frameID && opcode == 3 {
						sawCapture = true
						if err := writeExtTestEvent(conn, frameID, 4, nil); err != nil {
							serverDone <- err
							return
						}
					}
				}
			}()
			ctx, err := wl.Connect(sock)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			ctx.Register(shm)
			ctx.Register(sess)
			backend := &ExtCaptureBackend{session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)}, shm: shm, sessProxy: sess, outputScale: 1}
			captureCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = op.run(captureCtx, backend)
			cancel()
			if err == nil || !strings.Contains(err.Error(), "frame failed") {
				t.Fatalf("failed frame not recognized: %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("frame capture did not close socket")
			}
		})
	}
}
