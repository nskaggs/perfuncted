package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureRejectsUntrustedWaylandConstraintsBeforeSharedMemoryAllocation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sessionEvents []struct {
			opcode uint32
			data   []byte
		}
		want string
	}{
		{"zero size", []struct {
			opcode uint32
			data   []byte
		}{{1, wlUint32Payload(0)}}, "buffer size"},
		{"empty size payload", []struct {
			opcode uint32
			data   []byte
		}{{0, []byte{2, 0, 0, 0}}}, "malformed"},
		{"empty format payload", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(2, 2)}, {1, []byte{}}}, "malformed"},
		{"session stopped", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(2, 2)}, {1, wlUint32Payload(0)}, {5, nil}}, "stopped"},
		{"oversized width", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(0x40000000, 2)}, {1, wlUint32Payload(0)}}, "width is too large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, lintShadow56 := listener.AcceptUnix()
				if lintShadow56 != nil {
					done <- lintShadow56
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				first := make([]byte, 12)
				if _, lintShadow64 := io.ReadFull(conn, first); lintShadow64 != nil {
					done <- lintShadow64
					return
				}
				callback := wl.Uint32(first[8:])
				for _, event := range tc.sessionEvents {
					if lintShadow70 := writeExtTestEvent(conn, 3, event.opcode, event.data); lintShadow70 != nil {
						done <- lintShadow70
						return
					}
				}
				if lintShadow75 := writeExtTestEvent(conn, callback, 0, nil); lintShadow75 != nil {
					done <- lintShadow75
					return
				}
				for {
					var h [8]byte
					if _, lintShadow81 := io.ReadFull(conn, h[:]); lintShadow81 != nil {
						if errors.Is(lintShadow81, io.EOF) {
							done <- nil
						} else {
							done <- lintShadow81
						}
						return
					}
					size := int(wl.Uint32(h[4:]) >> 16)
					if size < 8 || size > 2048 {
						done <- fmt.Errorf("untrusted wire size %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, lintShadow95 := io.ReadFull(conn, body); lintShadow95 != nil {
						done <- lintShadow95
						return
					}
					// A malformed or impossible constraint must never create a buffer.
					if sender := wl.Uint32(h[:4]); sender == 2 {
						done <- fmt.Errorf("invalid capture allocated shm buffer after constraints failure")
						return
					}
				}
			}()
			wlctx, err := wl.Connect(socket)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			wlctx.Register(shm)
			wlctx.Register(sess)
			if shm.ID() != 2 || sess.ID() != 3 {
				t.Fatalf("unexpected fixture IDs %d,%d", shm.ID(), sess.ID())
			}
			b := &ExtCaptureBackend{session: &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)}, shm: shm, sessProxy: sess}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, grabErr := b.GrabFullHash(ctx)
			cancel()
			if grabErr == nil || !strings.Contains(grabErr.Error(), tc.want) {
				t.Fatalf("untrusted constraints %s returned %v", tc.name, grabErr)
			}
			if b.cachedBuf != nil || b.cachedFd != nil {
				t.Fatal("bad constraints allocated shared memory")
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("backend failed to close rejected session")
			}
		})
	}
}
