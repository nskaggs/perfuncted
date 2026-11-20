package screen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrRegistryRoundTripFailureDropsCachedConnection(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
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
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverDone <- err
			return
		}
		for _, wantOpcode := range []uint32{1, 0} { // get_registry, then sync
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				serverDone <- err
				return
			}
			size := int(wl.Uint32(header[4:8]) >> 16)
			if wl.Uint32(header[:4]) != 1 || wl.Uint32(header[4:8])&0xffff != wantOpcode || size != 12 {
				serverDone <- fmt.Errorf("unexpected Wayland request: sender=%d opcode=%d size=%d", wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff, size)
				return
			}
			if _, err := io.CopyN(io.Discard, conn, int64(size-8)); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil // Closing without the sync callback makes the round trip fail.
	}()

	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = backend.withWlrContextContext(ctx, func(*wl.Context, context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "registry round-trip") {
		t.Fatalf("registry disconnect returned %v, want registry round-trip error", err)
	}
	if backend.ctx != nil {
		t.Fatal("backend retained the connection after registry round-trip failure")
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatal("synthetic compositor did not receive get_registry and sync")
	}
}

func TestWlrCaptureRejectsMissingRequiredRegistryGlobals(t *testing.T) {
	for _, missing := range []string{"wl_shm", "wl_output"} {
		t.Run(missing, func(t *testing.T) {
			socket := shortPrivateSocketPath(t, "wayland.sock")
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
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					serverDone <- err
					return
				}
				var registryID uint32
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						if err == io.EOF {
							serverDone <- nil
						} else {
							serverDone <- err
						}
						return
					}
					size := int(wl.Uint32(header[4:8]) >> 16)
					if size < 8 || size > 1024 {
						serverDone <- fmt.Errorf("invalid Wayland request size %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, err := io.ReadFull(conn, body); err != nil {
						serverDone <- err
						return
					}
					sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff
					switch {
					case sender == 1 && opcode == 1:
						registryID = wl.Uint32(body)
					case sender == 1 && opcode == 0:
						for _, global := range []struct {
							name, version uint32
							iface         string
						}{
							{41, 3, "zwlr_screencopy_manager_v1"},
							{42, 1, "wl_shm"},
							{43, 4, "wl_output"},
						} {
							if global.iface == missing {
								continue
							}
							if err := sendWlrTestEvent(conn, registryID, 0, wlrGlobalPayload(global.name, global.iface, global.version)); err != nil {
								serverDone <- err
								return
							}
						}
						if err := sendWlrTestEvent(conn, wl.Uint32(body), 0, wlUint32Payload(0)); err != nil {
							serverDone <- err
							return
						}
					case sender == registryID && opcode == 0:
						// wl_shm may be bound before the client rejects the missing output.
					default:
						serverDone <- fmt.Errorf("unexpected Wayland request sender=%d opcode=%d", sender, opcode)
						return
					}
				}
			}()

			backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			hash, captureErr := backend.GrabFullHash(ctx)
			if hash != 0 || captureErr == nil || !strings.Contains(captureErr.Error(), "wl_shm or wl_output missing") {
				t.Fatalf("missing %s global returned hash=%08x err=%v", missing, hash, captureErr)
			}
			if backend.ctx != nil {
				t.Fatal("backend retained its connection after missing required global")
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-ctx.Done():
				t.Fatal("synthetic compositor did not observe the registry request")
			}
		})
	}
}

func TestWlrCaptureReturnsDispatchErrorWhenCompositorDisconnects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
	}{
		{name: "before frame setup completes", outcome: "disconnect after capture request"},
		{name: "after copy is issued", outcome: "disconnect after copy request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := shortPrivateSocketPath(t, "wayland.sock")
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
				serverDone <- wlrFakeCompositor(conn, tc.outcome)
			}()

			backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			hash, err := backend.GrabFullHash(ctx)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "dispatch") {
				t.Fatalf("compositor disconnect published hash=%08x err=%v, want dispatch error", hash, err)
			}
			if backend.ctx != nil {
				t.Fatal("backend retained the connection after compositor disconnect")
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-ctx.Done():
				t.Fatal("synthetic compositor did not observe the capture phase before disconnecting")
			}
		})
	}
}

type wlrErrCheckGateContext struct {
	context.Context
	armed        atomic.Bool
	checkReached chan struct{}
	resume       <-chan struct{}
	once         sync.Once
}

func (c *wlrErrCheckGateContext) Err() error {
	if c.armed.Load() {
		c.once.Do(func() { close(c.checkReached) })
		<-c.resume
	}
	return c.Context.Err()
}

func TestWlrCaptureCancellationAfterCopyDoesNotPublishHash(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	copyObserved := make(chan struct{}, 1)
	postCopyEventPermit := make(chan struct{})
	var postCopyEventOnce sync.Once
	releasePostCopyEvent := func() { postCopyEventOnce.Do(func() { close(postCopyEventPermit) }) }
	defer releasePostCopyEvent()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- wlrFakeCompositorWithCopySignal(conn, "cancel after copy request", copyObserved, postCopyEventPermit)
	}()

	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	operationCtx, cancelOperation := context.WithCancel(ctx)
	defer cancelOperation()
	resumeErrCheck := make(chan struct{})
	var resumeErrCheckOnce sync.Once
	releaseErrCheck := func() { resumeErrCheckOnce.Do(func() { close(resumeErrCheck) }) }
	defer releaseErrCheck()
	gateCtx := &wlrErrCheckGateContext{Context: operationCtx, checkReached: make(chan struct{}), resume: resumeErrCheck}
	type captureResult struct {
		hash uint32
		err  error
	}
	result := make(chan captureResult, 1)
	go func() {
		hash, captureErr := backend.GrabFullHash(gateCtx)
		result <- captureResult{hash: hash, err: captureErr}
	}()

	select {
	case <-copyObserved:
		gateCtx.armed.Store(true)
		releasePostCopyEvent()
	case <-ctx.Done():
		t.Fatalf("compositor did not observe the capture copy request: %v", ctx.Err())
	}
	select {
	case <-gateCtx.checkReached:
		cancelOperation()
		releaseErrCheck()
	case <-ctx.Done():
		t.Fatalf("capture did not return to its cancellation check after an output event: %v", ctx.Err())
	}
	select {
	case got := <-result:
		if got.hash != 0 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled capture published hash=%08x err=%v", got.hash, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("capture did not return after caller cancellation: %v", ctx.Err())
	}
	if backend.ctx != nil {
		t.Fatal("backend retained the connection after caller cancellation")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatal("synthetic compositor did not observe frame cleanup after cancellation")
	}
}

func TestWlrCaptureReallocatesSharedMemoryWhenGeometryChanges(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
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
		serverDone <- wlrFakeCompositor(conn, "geometry change after first")
	}()

	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if _, err := backend.GrabFullHash(ctx); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if backend.cachedBI != (bufInfo{format: 0, width: 2, height: 2, stride: 8}) {
		t.Fatalf("first shared-memory geometry = %+v", backend.cachedBI)
	}
	if _, err := backend.GrabFullHash(ctx); err != nil {
		t.Fatalf("capture after geometry change: %v", err)
	}
	if backend.cachedBI != (bufInfo{format: 0, width: 3, height: 2, stride: 12}) || len(backend.cachedBuf) != 24 || backend.cachedFd == nil {
		t.Fatalf("shared memory was not recreated for new geometry: info=%+v bytes=%d fd=%v", backend.cachedBI, len(backend.cachedBuf), backend.cachedFd)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatal("synthetic compositor did not terminate after capture cleanup")
	}
}

func TestWlrCaptureCancellationAfterOperationAdmissionDoesNotPublishHash(t *testing.T) {
	backend := newWlrScreencopyBackendWithContextConnector("already-connected", wl.ConnectContext, time.Minute)
	backend.ctx = &wl.Context{}
	backend.mgrProxy = &wlRawProxy{}
	backend.output = &wlRawProxy{}
	backend.shm = &wl.Shm{}
	t.Cleanup(func() { _ = backend.Close() })

	ctx := &wlrCancelAtCaptureCheckContext{Context: context.Background(), done: make(chan struct{})}
	hash, err := backend.GrabFullHash(ctx)
	if hash != 0 || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "capture canceled") {
		t.Fatalf("capture cancellation published hash=%08x err=%v", hash, err)
	}
	if backend.ctx != nil {
		t.Fatal("backend retained the connection after cancellation")
	}
}

// The third caller-context check occurs after the serialized backend operation
// is admitted and before capture_frame is sent. Cancel at that boundary so the
// test verifies that no pixels are published for an abandoned operation.
type wlrCancelAtCaptureCheckContext struct {
	context.Context
	done      chan struct{}
	errChecks atomic.Int32
	cancel    sync.Once
}

func (ctx *wlrCancelAtCaptureCheckContext) Done() <-chan struct{} { return ctx.done }

func (ctx *wlrCancelAtCaptureCheckContext) Err() error {
	if ctx.errChecks.Add(1) >= 3 {
		ctx.cancel.Do(func() { close(ctx.done) })
		return context.Canceled
	}
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}
