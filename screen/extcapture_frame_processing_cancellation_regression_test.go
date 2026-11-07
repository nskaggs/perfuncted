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

// A capture canceled while consuming the compositor's completed pixel buffer
// cannot report success: the caller no longer owns a publishable result.
func TestExtCaptureFrameProcessingCancellationCannotPublishSuccess(t *testing.T) {
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
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		first := make([]byte, 12)
		if _, err := io.ReadFull(conn, first); err != nil {
			serverDone <- err
			return
		}
		callback := wl.Uint32(first[8:12])
		for _, ev := range []struct {
			sender, opcode uint32
			body           []byte
		}{
			{3, 0, wlUint32Payload(2, 2)},
			{3, 1, wlUint32Payload(0)},
			{callback, 0, nil},
		} {
			if err := writeExtTestEvent(conn, ev.sender, ev.opcode, ev.body); err != nil {
				serverDone <- err
				return
			}
		}
		var frame uint32
		for {
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				serverDone <- err
				return
			}
			length := int(wl.Uint32(header[4:8])>>16) - 8
			if length < 0 || length > 4096 {
				serverDone <- io.ErrUnexpectedEOF
				return
			}
			body := make([]byte, length)
			if _, err := io.ReadFull(conn, body); err != nil {
				serverDone <- err
				return
			}
			sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff
			if sender == 3 && opcode == 0 && len(body) >= 4 {
				frame = wl.Uint32(body[:4])
			}
			if frame != 0 && sender == frame && opcode == 3 {
				serverDone <- writeExtTestEvent(conn, frame, 3, nil)
				return
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
	captureCtx, cancel := context.WithCancel(context.Background())
	processed := false
	err = backend.grabInternal(captureCtx, func(pixels []byte, w, h, stride int) error { processed = true; cancel(); return nil })
	if !processed {
		t.Fatal("capture never processed a completed frame")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled frame processing was acknowledged: %v", err)
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compositor did not finish")
	}
}
