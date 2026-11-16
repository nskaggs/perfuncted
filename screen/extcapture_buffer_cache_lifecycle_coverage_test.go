package screen

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureReusesAndReplacesCachedBufferByGeometry(t *testing.T) {
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
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		sizes := [][2]uint32{{2, 2}, {2, 2}, {3, 2}}
		syncs, captures := 0, 0
		var frameID uint32
		for {
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				if errors.Is(err, io.EOF) && syncs == len(sizes) && captures == len(sizes) {
					serverDone <- nil
				} else {
					serverDone <- err
				}
				return
			}
			size := int(wl.Uint32(header[4:]) >> 16)
			if size < 8 || size > 4096 {
				serverDone <- io.ErrUnexpectedEOF
				return
			}
			body := make([]byte, size-8)
			if _, err := io.ReadFull(conn, body); err != nil {
				serverDone <- err
				return
			}
			sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:])&0xffff
			if sender == 1 && opcode == 0 {
				if len(body) != 4 || syncs >= len(sizes) {
					serverDone <- io.ErrUnexpectedEOF
					return
				}
				callback := wl.Uint32(body)
				constraints := []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(sizes[syncs][0], sizes[syncs][1])},
					{3, 1, wlUint32Payload(0)},
					{callback, 0, nil},
				}
				for _, event := range constraints {
					if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
						serverDone <- err
						return
					}
				}
				syncs++
				continue
			}

			if sender == 3 && opcode == 0 {
				if len(body) != 4 {
					serverDone <- io.ErrUnexpectedEOF
					return
				}
				frameID = wl.Uint32(body)
				continue
			}
			if frameID != 0 && sender == frameID && opcode == 3 {
				if err := writeExtTestEvent(conn, sender, 3, nil); err != nil {
					serverDone <- err
					return
				}
				captures++
				frameID = 0
			}
		}
	}()

	wlctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	shm := &wl.Shm{}
	session := &wlRawProxy{}
	wlctx.Register(shm)
	wlctx.Register(session)
	if shm.ID() != 2 || session.ID() != 3 {
		t.Fatalf("unexpected fixture objects: shm=%d session=%d", shm.ID(), session.ID())
	}
	backend := &ExtCaptureBackend{
		session:     &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
		shm:         shm,
		sessProxy:   session,
		outputScale: 1,
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close capture backend: %v", err)
		}
	}()

	capture := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := backend.GrabFullHash(ctx); err != nil {
			t.Fatalf("capture failed: %v", err)
		}
	}

	capture()
	firstFile := backend.cachedFd
	if firstFile == nil || len(backend.cachedBuf) == 0 {
		t.Fatal("first capture did not retain its shared-memory mapping")
	}
	firstBuffer := &backend.cachedBuf[0]

	capture()
	if backend.cachedFd != firstFile || &backend.cachedBuf[0] != firstBuffer {
		t.Fatal("same-size capture replaced its cached shared-memory mapping")
	}

	capture()
	if backend.cachedFd == firstFile || backend.cachedBI.width != 3 || backend.cachedBI.height != 2 {
		t.Fatalf("changed-size capture retained stale mapping: file=%p geometry=%dx%d", backend.cachedFd, backend.cachedBI.width, backend.cachedBI.height)
	}
	if _, err := firstFile.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("replaced mapping descriptor remained open: %v", err)
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
		t.Fatal("synthetic compositor did not observe session teardown")
	}
}
