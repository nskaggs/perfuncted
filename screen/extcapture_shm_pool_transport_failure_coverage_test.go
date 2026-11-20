package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureDoesNotPublishHashWhenSharedMemoryPoolTransportFails(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		request := make([]byte, 12)
		if _, readErr := io.ReadFull(conn, request); readErr != nil {
			_ = conn.Close()
			serverDone <- fmt.Errorf("read initial sync request: %w", readErr)
			return
		}
		callback := wl.Uint32(request[8:])
		for _, event := range []struct {
			sender, opcode uint32
			body           []byte
		}{
			{3, 0, wlUint32Payload(2, 2)},
			{3, 1, wlUint32Payload(0)},
			{callback, 0, nil},
		} {
			if writeErr := writeExtTestEvent(conn, event.sender, event.opcode, event.body); writeErr != nil {
				_ = conn.Close()
				serverDone <- fmt.Errorf("write session constraints: %w", writeErr)
				return
			}
		}
		if closeErr := conn.Close(); closeErr != nil {
			serverDone <- fmt.Errorf("close after session constraints: %w", closeErr)
			return
		}
		serverDone <- nil
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
		t.Fatalf("unexpected fixture object IDs: shm=%d session=%d", shm.ID(), session.ID())
	}
	backend := &ExtCaptureBackend{
		session:   &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
		shm:       shm,
		sessProxy: session,
	}

	type captureResult struct {
		hash uint32
		err  error
	}
	captureDone := make(chan captureResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		hash, captureErr := backend.GrabFullHash(ctx)
		captureDone <- captureResult{hash: hash, err: captureErr}
	}()

	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not deliver session constraints: %v", ctx.Err())
	}
	select {
	case got := <-captureDone:
		// The compositor closes after sending constraints. Depending on which
		// queued Wayland request reaches the kernel first, the disconnect can
		// surface during pool creation or a subsequent frame dispatch. The
		// invariant is no published hash after a failed transport.
		transportLost := errors.Is(got.err, io.EOF) || errors.Is(got.err, syscall.EPIPE) || errors.Is(got.err, syscall.ECONNRESET)
		if got.hash != 0 || !transportLost {
			t.Fatalf("failed shared-memory setup published hash=%08x err=%v", got.hash, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("capture did not return after pool transport closed: %v", ctx.Err())
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close after failed pool setup: %v", err)
	}
}
