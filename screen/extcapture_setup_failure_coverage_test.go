package screen

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureFailsClosedOnSessionRoundTripAndBufferGeometryErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disconnect bool
		width      uint32
		height     uint32
		want       string
	}{
		{name: "session round-trip disconnect", disconnect: true, want: "session round-trip"},
		{name: "buffer allocation overflow", width: 1, height: ^uint32(0), want: "invalid buffer geometry"},
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
				conn, err := listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				request := make([]byte, 12)
				if _, err := io.ReadFull(conn, request); err != nil {
					serverDone <- err
					return
				}
				if tc.disconnect {
					serverDone <- nil
					return
				}
				callback := wl.Uint32(request[8:])
				for _, event := range []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(tc.width, tc.height)},
					{3, 1, wlUint32Payload(0)},
					{callback, 0, nil},
				} {
					if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
						serverDone <- err
						return
					}
				}
				var header [8]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					if err == io.EOF {
						serverDone <- nil
					} else {
						serverDone <- err
					}
					return
				}
				serverDone <- fmt.Errorf("invalid geometry sent unexpected Wayland request from object %d", wl.Uint32(header[:4]))
			}()

			wlctx, err := wl.Connect(socket)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			session := &wlRawProxy{}
			wlctx.Register(shm)
			wlctx.Register(session)
			backend := &ExtCaptureBackend{
				session:   &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
				shm:       shm,
				sessProxy: session,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			hash, captureErr := backend.GrabFullHash(ctx)
			cancel()
			if hash != 0 || captureErr == nil || !strings.Contains(captureErr.Error(), tc.want) {
				t.Fatalf("invalid capture returned hash=%08x err=%v, want error containing %q", hash, captureErr, tc.want)
			}
			if backend.cachedBuf != nil || backend.cachedFd != nil {
				t.Fatal("failed capture retained a shared-memory mapping")
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("synthetic compositor did not finish the failed capture")
			}
		})
	}
}
