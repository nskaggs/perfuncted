package screen

import (
	"context"
	"image"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A Wayland screencopy frame advertises its memory geometry before the client
// allocates shared memory. Malicious or corrupted metadata must be rejected
// by every public image/hash operation, without publishing data or retaining
// the invalid buffer for a later capture.
func TestWlrCaptureRejectsUntrustedBufferGeometryOnWire(t *testing.T) { //nolint:gocyclo // Three untrusted geometries are checked across every capture API and complete socket teardown.
	for _, outcome := range []string{"undersized stride", "zero-height buffer", "oversized framebuffer"} {
		t.Run(outcome, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				run  func(context.Context, *WlrScreencopyBackend) error
			}{
				{"image", func(ctx context.Context, b *WlrScreencopyBackend) error {
					img, err := b.Grab(ctx, image.Rectangle{})
					if img != nil {
						t.Fatal("malformed geometry returned screenshot")
					}
					return err
				}},
				{"full hash", func(ctx context.Context, b *WlrScreencopyBackend) error {
					hash, err := b.GrabFullHash(ctx)
					if hash != 0 {
						t.Fatalf("malformed geometry published hash: %x", hash)
					}
					return err
				}},
				{"region hash", func(ctx context.Context, b *WlrScreencopyBackend) error {
					hash, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 1, 1))
					if hash != 0 {
						t.Fatalf("malformed geometry published region hash: %x", hash)
					}
					return err
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
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
						serverDone <- wlrFakeCompositor(conn, outcome)
					}()
					backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					captureErr := tc.run(ctx, backend)
					if captureErr == nil || !strings.Contains(captureErr.Error(), "invalid buffer geometry") {
						t.Errorf("untrusted geometry was not rejected: %v", captureErr)
					}
					if backend.cachedFd != nil || len(backend.cachedBuf) != 0 {
						t.Error("invalid geometry allocated persistent shared-memory pixels")
					}
					if err := backend.Close(); err != nil {
						t.Fatal(err)
					}
					select {
					case serverErr := <-serverDone:
						if serverErr != nil {
							t.Fatalf("synthetic compositor cleanup failed: %v", serverErr)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("malformed-frame compositor was not released")
					}
				})
			}
		})
	}
}
