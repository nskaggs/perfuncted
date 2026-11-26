package screen

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/internal/wl"
	"image"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Revocation that arrives during the final pixel consumer must invalidate the
// entire capture transaction. A protocol-ready frame is not permission to
// return its private pixels after the caller has revoked the operation.
func TestWlrScreencopyRejectsCancellationDuringPixelProcessing(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
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
		serverDone <- wlrFakeCompositor(conn, "")
	}()
	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observedPixels := false
	captureErr := backend.captureFrame(ctx, func(pixels []byte, bi bufInfo) error {
		if bi.width != 2 || bi.height != 2 || len(pixels) != 16 {
			t.Fatalf("synthetic screenshot geometry: %+v bytes=%d", bi, len(pixels))
		}
		observedPixels = true
		cancel()
		return nil
	})
	if !observedPixels {
		t.Fatalf("capture never reached private pixel processing: %v", captureErr)
	}
	if !errors.Is(captureErr, context.Canceled) {
		t.Fatalf("capture reported success after caller revoked pixels: %v", captureErr)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("synthetic compositor not released")
	}
}

// Minimal compositor validates the real socket connection, registry enumeration,
// and sync callback used by the public wlr screenshot constructor.
func TestWlrConstructorRealWaylandSocketProtocolDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, advertised string
		wantSuccess      bool
	}{
		{"screencopy advertised", "zwlr_screencopy_manager_v1", true},
		{"no screencopy advertised", "wl_seat", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("unix", testutil.ShortSocketPath(t, "wl.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				var registry uint32
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						if errors.Is(err, io.EOF) {
							done <- nil
						} else {
							done <- err
						}
						return
					}
					wire := wl.Uint32(header[4:])
					size := int(wire >> 16)
					if size < 8 || size > 65535 {
						done <- errors.New("invalid request size")
						return
					}
					payload := make([]byte, size-8)
					if _, err := io.ReadFull(conn, payload); err != nil {
						done <- err
						return
					}
					if wl.Uint32(header[:4]) != 1 || len(payload) != 4 {
						done <- errors.New("unexpected request")
						return
					}
					switch wire & 0xffff {
					case 1:
						registry = wl.Uint32(payload)
					case 0:
						callback := wl.Uint32(payload)
						iface := tc.advertised
						length := len(iface) + 1
						padded := (length + 3) &^ 3
						event := make([]byte, 8+padded+4)
						wl.PutUint32(event[0:4], 52)
						wl.PutUint32(event[4:8], uint32(length))
						copy(event[8:], iface)
						wl.PutUint32(event[8+padded:], 1)
						if err := sendWlrTestEvent(conn, registry, 0, event); err != nil {
							done <- err
							return
						}
						if err := sendWlrTestEvent(conn, callback, 0, make([]byte, 4)); err != nil {
							done <- err
							return
						}
					default:
						done <- errors.New("unexpected request opcode")
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := NewWlrScreencopyBackendForSocketContext(ctx, listener.Addr().String())
			if tc.wantSuccess {
				if err != nil || b == nil {
					t.Fatalf("advertised protocol not accepted: backend=%v err=%v", b, err)
				}
				if closeErr := b.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			} else if b != nil || err == nil || !strings.Contains(err.Error(), "does not advertise") {
				t.Fatalf("unadvertised protocol accepted: backend=%v err=%v", b, err)
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("constructor leaked Wayland session socket")
			}
		})
	}
}

func sendWlrTestEvent(conn net.Conn, id, opcode uint32, body []byte) error {
	msg := make([]byte, 8+len(body))
	wl.PutUint32(msg[:4], id)
	wl.PutUint32(msg[4:8], uint32(len(msg))<<16|opcode)
	copy(msg[8:], body)
	_, err := conn.Write(msg)
	return err
}

func TestWlrBackendMissingSessionClosedAndCanceledOperations(t *testing.T) {
	var absent *WlrScreencopyBackend
	if reports := absent.Diagnostics(); len(reports) != 1 || reports[0] != "backend unavailable" {
		t.Fatalf("nil diagnostics=%v", reports)
	}
	if w, h, err := absent.ResolutionWithContext(context.Background()); w != 0 || h != 0 || err == nil {
		t.Fatalf("nil resolution=%d,%d %v", w, h, err)
	}
	if err := absent.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil {
		t.Fatal("nil backend accepted context operation")
	}
	if err := absent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWlrFailedConnectionRecordsHealthAndRejectsClosedOperations(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector(filepath.Join(t.TempDir(), "does-not-exist"), nil, 0)
	if b.ttl <= 0 || b.connect == nil {
		t.Fatalf("defaulted backend invalid: ttl=%s", b.ttl)
	}
	if reports := b.Diagnostics(); len(reports) < 3 || !strings.Contains(strings.Join(reports, ";"), "no recorded failures") {
		t.Fatalf("fresh health=%v", reports)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.captureFrame(cancelled, func([]byte, bufInfo) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture accepted: %v", err)
	}
	if w, h, err := b.ResolutionWithContext(cancelled); w != 0 || h != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution=%d,%d %v", w, h, err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("unreachable compositor accepted: %v", err)
	}
	if reports := b.Diagnostics(); !strings.Contains(strings.Join(reports, ";"), "last failure") {
		t.Fatalf("failed connection not recorded: %v", reports)
	}
	if w, h, err := b.Resolution(); w != 0 || h != 0 || err == nil {
		t.Fatalf("unreachable resolution=%d,%d %v", w, h, err)
	}
	assertWlrCloseAndNoReuse(t, b)
}

func TestWlrPublicSocketConstructorRejectsInvalidTargetsBeforeTransport(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := NewWlrScreencopyBackendForSocketContext(cancelled, "invalid"); b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor=%v %v", b, err)
	}
	if b, err := NewWlrScreencopyBackendForSocketContext(context.Background(), ""); b != nil || err == nil {
		t.Fatalf("empty socket accepted: %v %v", b, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.sock")
	if b, err := NewWlrScreencopyBackendForSocketContext(context.Background(), missing); b != nil || err == nil {
		t.Fatalf("unreachable socket accepted: %v %v", b, err)
	}
}

func TestWlrCachedResolutionPreservesMeasuredDimensionsWithoutCapture(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("not-connected", nil, time.Minute)
	b.pW, b.pH = 1920, 1080
	w, h, err := b.ResolutionWithContext(context.Background())
	if err != nil || w != 1920 || h != 1080 {
		t.Fatalf("cached resolution %dx%d %v", w, h, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWlrRawProxyDispatchContextAccessorAndFailedFrameBoundary(t *testing.T) {
	proxy := &wlRawProxy{}
	var opcode uint32
	proxy.dispatchFn = func(next uint32, _ int, _ []byte) { opcode = next }
	proxy.SetID(42)
	if proxy.ID() != 42 {
		t.Fatalf("proxy identity=%d", proxy.ID())
	}
	proxy.Dispatch(3, -1, nil)
	if opcode != 3 {
		t.Fatalf("failed frame not dispatched: %d", opcode)
	}
	var bi bufInfo
	ready, failed, done := false, false, false
	applyWlrFrameEvent(&bi, &ready, &failed, &done, 6, nil)
	if !done || failed || ready {
		t.Fatalf("buffer completion state ready=%v failed=%v done=%v", ready, failed, done)
	}
	_ = proxy.Ctx()
	_ = image.Rectangle{}
}

func assertWlrCloseAndNoReuse(t *testing.T, b *WlrScreencopyBackend) {
	t.Helper()

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if reports := b.Diagnostics(); !strings.Contains(strings.Join(reports, ";"), "connection health: closed") {
		t.Fatalf("closed diagnostics=%v", reports)
	}
	if w, h, err := b.ResolutionWithContext(context.Background()); w != 0 || h != 0 || err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed resolution=%d,%d %v", w, h, err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed backend accepted operation: %v", err)
	}
}

// Empty region hashes must use the full-frame algorithm, not a special
// zero checksum. An unknown output scale must not multiply a requested
// logical crop into a different physical region.
func TestWlrWireEmptyRegionHashAndUnknownScaleAreConsistent(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wlr.sock")
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

// A compositor's malformed signed scale update must not change a known
// logical-to-physical coordinate mapping for subsequent screen captures.
func TestWlrScreencopyRejectsInvalidScaleAfterValidOutputGeometry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale uint32
	}{
		{"zero", 0},
		{"negative one", ^uint32(0)},
		{"minimum signed", 1 << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			valid := make([]byte, 4)
			wl.PutUint32(valid, 2)
			b.applyOutputEvent(3, valid)
			malformed := make([]byte, 4)
			wl.PutUint32(malformed, tc.scale)
			b.applyOutputEvent(3, malformed)
			if b.scale != 2 {
				t.Fatalf("invalid scale %08x changed established scale to %d", tc.scale, b.scale)
			}
		})
	}
}

func TestWlrResolutionFailsAfterBackendCloseEvenWhenDimensionsCached(t *testing.T) {
	b := &WlrScreencopyBackend{pW: 1920, pH: 1080}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if w, h, err := b.ResolutionWithContext(context.Background()); err == nil || w != 0 || h != 0 {
		t.Fatalf("closed backend leaked stale resolution %dx%d: %v", w, h, err)
	}
}
func TestWlrUntrustedConnectorCannotReturnNilConnectionAsSuccess(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("synthetic-nil", func(context.Context, string) (*wl.Context, error) { return nil, nil }, time.Hour)
	defer b.Close()
	if img, err := b.Grab(context.Background(), image.Rectangle{}); img != nil || err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("connector nil-success reached capture: img=%v err=%v", img, err)
	}
}

func TestWlrReconnectWithoutScreencopyProtocolFailsClosed(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("mock-reconnected-socket", func(string) (*wl.Context, error) { return &wl.Context{}, nil }, time.Minute)
	defer b.Close()
	img, err := b.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("missing screencopy manager accepted: img=%v err=%v", img, err)
	}
	if b.ctx != nil {
		t.Fatal("failed capture retained invalid Wayland connection")
	}
	for _, capture := range []struct {
		name string
		run  func() error
	}{
		{"full hash", func() error { _, err := b.GrabFullHash(context.Background()); return err }},
		{"region hash", func() error { _, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 2, 2)); return err }},
	} {
		t.Run(capture.name, func(t *testing.T) {
			if err := capture.run(); err == nil || !strings.Contains(err.Error(), "protocol") {
				t.Fatalf("missing protocol accepted: %v", err)
			}
		})
	}
}

// wl_output.mode advertises available display modes, not only the active one.
// A non-current or invalid signed size must not replace the last confirmed
// screenshot geometry, since ResolutionWithContext can return these cached
// values without capturing another frame.
func TestWlrScreencopyIgnoresNoncurrentAndInvalidOutputModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flags, w, h uint32
	}{
		{"noncurrent alternate mode", 0, 1280, 720},
		{"preferred but not current", 2, 2560, 1440},
		{"negative width", wl.OutputModeCurrent, ^uint32(0), 1080},
		{"negative height", wl.OutputModeCurrent, 1920, ^uint32(0)},
		{"zero width", wl.OutputModeCurrent, 0, 1080},
		{"zero height", wl.OutputModeCurrent, 1920, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			send := func(flags, w, h uint32) {
				payload := make([]byte, 16)
				wl.PutUint32(payload[0:4], flags)
				wl.PutUint32(payload[4:8], w)
				wl.PutUint32(payload[8:12], h)
				wl.PutUint32(payload[12:16], 60000)
				b.applyOutputEvent(1, payload)
			}
			send(wl.OutputModeCurrent, 1920, 1080)
			send(tc.flags, tc.w, tc.h)
			if b.pW != 1920 || b.pH != 1080 {
				t.Fatalf("non-authoritative mode replaced current output geometry with %dx%d", b.pW, b.pH)
			}
		})
	}
}

// A revoked capture cannot open a new Wayland connection or run its callback,
// even if a connector would otherwise succeed. Health records the refusal.
func TestWlrCanceledOperationNeverConnectsOrExecutesCapture(t *testing.T) {
	connects, calls := 0, 0
	b := newWlrScreencopyBackendWithContextConnector("fake", func(context.Context, string) (*wl.Context, error) {
		connects++
		return &wl.Context{}, nil
	}, time.Minute)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.withWlrContextContext(ctx, func(*wl.Context, context.Context) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || connects != 0 || calls != 0 {
		t.Fatalf("canceled capture crossed transport boundary: err=%v connects=%d callbacks=%d", err, connects, calls)
	}
	if got := strings.Join(b.Diagnostics(), "; "); !strings.Contains(got, "context canceled") || !strings.Contains(got, "reconnect required") {
		t.Fatalf("canceled operation not reflected in backend diagnostics: %s", got)
	}
}

// Close owns the current capture's cancel function even when no connection
// was yet installed. Cancellation must happen once and stay idempotent.
func TestWlrCloseCancelsActiveCaptureAndDoesNotReviveIt(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("fake", nil, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.activeCancel = cancel
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("close did not cancel active capture: %v", ctx.Err())
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed capture backend accepted another operation: %v", err)
	}
}

// A compositor's frame.ready event arriving before the capture copy request
// cannot authorize pixels. Otherwise a producer can skip the copy entirely
// and the consumer would publish an uninitialized shared-memory screenshot.
func TestWlrScreencopyRejectsReadyBeforeCopyProtocolViolation(t *testing.T) {
	for _, outcome := range []string{"ready before buffer", "premature ready"} {
		t.Run(outcome, func(t *testing.T) {
			testWlrReadyBeforeCopyFailsClosed(t, outcome)
		})
	}
}

func testWlrReadyBeforeCopyFailsClosed(t *testing.T, outcome string) {
	t.Helper()
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	server, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := server.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- wlrFakeCompositor(conn, outcome)
	}()
	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if img != nil || err == nil {
		t.Errorf("pre-copy ready was accepted as a completed screenshot: img=%v err=%v", img, err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("synthetic compositor leaked")
	}
}

// Queued initialization can sit behind a prior capture's backend lock.
// Neither cancellation nor shutdown while queued may connect a new Wayland
// socket or allow an operation on already-retired authorization.
func TestWlrQueuedConnectionCancelsBeforeConnectorCall(t *testing.T) {
	var connectCalls atomic.Int32
	backend := NewWlrScreencopyBackendWithConnector("unused", func(string) (*wl.Context, error) { connectCalls.Add(1); return &wl.Context{}, nil }, time.Minute)
	defer backend.Close()
	entered := make(chan struct{})
	backend.initJanitor = func() { close(entered) }
	backend.ctxMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- backend.withWlrContextContext(ctx, func(*wl.Context, context.Context) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		cancel()
		t.Fatal("operation did not reach queued initialization")
	}
	cancel()
	backend.ctxMu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled queued connection returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued connection did not finish")
	}
	if calls := connectCalls.Load(); calls != 0 {
		t.Fatalf("canceled operation invoked connector %d times", calls)
	}
	backend.ctxMu.Lock()
	hasConnection := backend.ctx != nil
	backend.ctxMu.Unlock()
	if hasConnection {
		t.Fatal("canceled operation cached an unauthorized connection")
	}
}

func TestWlrCloseDuringQueuedConnectionPreventsNewTransport(t *testing.T) {
	var connectCalls atomic.Int32
	backend := NewWlrScreencopyBackendWithConnector("unused", func(string) (*wl.Context, error) { connectCalls.Add(1); return &wl.Context{}, nil }, time.Minute)
	entered := make(chan struct{})
	backend.initJanitor = func() { close(entered) }
	backend.ctxMu.Lock()
	pending := make(chan error, 1)
	go func() {
		pending <- backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		t.Fatal("operation did not reach queued initialization")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- backend.Close() }()
	select {
	case <-backend.done:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		t.Fatal("Close did not retire backend")
	}
	backend.ctxMu.Unlock()
	select {
	case err := <-pending:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("queued call survived backend Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued call never returned")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not release queued call")
	}
	if calls := connectCalls.Load(); calls != 0 {
		t.Fatalf("closed backend invoked connector %d times", calls)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWlrCaptureCancellationAfterConnectSendsNoRegistryRequest(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
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
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverDone <- err
			return
		}
		var request [1]byte
		n, readErr := conn.Read(request[:])
		if n != 0 {
			serverDone <- errors.New("canceled capture sent a Wayland request")
			return
		}
		if !errors.Is(readErr, io.EOF) {
			serverDone <- errors.New("canceled capture did not close its Wayland connection")
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := newWlrScreencopyBackendWithContextConnector(socket, func(_ context.Context, path string) (*wl.Context, error) {
		conn, connectErr := wl.ConnectContext(context.Background(), path)
		if connectErr != nil {
			return nil, connectErr
		}
		// The socket is acquired, but protocol initialization must still honor
		// cancellation before it publishes or sends any registry request.
		cancel()
		return conn, nil
	}, time.Minute)
	t.Cleanup(func() { _ = backend.Close() })

	hash, captureErr := backend.GrabFullHash(ctx)
	if hash != 0 || !errors.Is(captureErr, context.Canceled) {
		t.Fatalf("canceled registry setup returned hash=%08x err=%v", hash, captureErr)
	}
	if backend.ctx != nil {
		t.Fatal("backend retained the connection after canceled registry setup")
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic compositor did not observe connection cleanup")
	}
}

func TestWlrRegistryRoundTripFailureDropsCachedConnection(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
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
			socket := testutil.ShortSocketPath(t, "wayland.sock")
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
			socket := testutil.ShortSocketPath(t, "wayland.sock")
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
	socket := testutil.ShortSocketPath(t, "wayland.sock")
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
	socket := testutil.ShortSocketPath(t, "wayland.sock")
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

func TestWlrResolutionUsesCapturedDimensionsWhenOutputModeIsAbsent(t *testing.T) {
	runtime := testutil.ShortSocketDir(t)
	socket := filepath.Join(runtime, "wayland.sock")
	server, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := server.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- wlrFakeCompositor(conn, "omit output mode")
	}()

	backend := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
	width, height, err := backend.Resolution()
	if err != nil {
		t.Fatalf("resolution from captured frame failed: %v", err)
	}
	if width != 2 || height != 2 {
		t.Fatalf("resolution from captured frame = %dx%d, want 2x2", width, height)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic Wayland server: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("compositor did not terminate")
	}
}

// Cache TTLs shorter than the ticker's minimum supported interval must not
// strand idle Wayland resources or continuously spin. After the idle context
// is reaped, the next authorized operation must create a fresh connection.
func TestWlrSubmillisecondCacheTTLReapsIdleContextAndReconnects(t *testing.T) {
	var connects atomic.Int32
	backend := newWlrScreencopyBackendWithContextConnector("synthetic-short-ttl", func(context.Context, string) (*wl.Context, error) {
		connects.Add(1)
		return &wl.Context{}, nil
	}, time.Nanosecond)
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err != nil {
		t.Fatalf("initial capture connection: %v", err)
	}
	if connects.Load() != 1 {
		t.Fatalf("unexpected initial connections: %d", connects.Load())
	}
	// The janitor owns ctxMu; inspect under the same lock and bound the wait.
	deadline := time.Now().Add(2 * time.Second)
	for {
		backend.ctxMu.Lock()
		idle := backend.ctx == nil
		backend.ctxMu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("submillisecond TTL never released idle Wayland context")
		}
		time.Sleep(time.Millisecond)
	}
	if err := backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err != nil {
		t.Fatalf("post-reap reconnection: %v", err)
	}
	if connects.Load() != 2 {
		t.Fatalf("cached expired context reused: connectors=%d, want two", connects.Load())
	}
}

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
					socket := testutil.ShortSocketPath(t, "wayland.sock")
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

func wlrGlobalPayload(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	padded := (n + 3) &^ 3
	result := make([]byte, 8+padded+4)
	wl.PutUint32(result[:4], name)
	wl.PutUint32(result[4:8], uint32(n))
	copy(result[8:], iface)
	wl.PutUint32(result[8+padded:], version)
	return result
}

// wlrFakeCompositor exercises the real registry, binder, screencopy frame,
// shared-memory setup, and ready/error dispatch over a Unix socket.
func wlrFakeCompositor(conn *net.UnixConn, frameOutcome string) error {
	return wlrFakeCompositorWithCopySignal(conn, frameOutcome, nil, nil)
}

func wlrFakeCompositorWithCopySignal(conn *net.UnixConn, frameOutcome string, copyObserved chan<- struct{}, postCopyEventPermit <-chan struct{}) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var registryID, managerID, outputID, frameID uint32
	var frameCount int
	var sawCapture, sawCopy bool
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			// A failed capture can close the transport with unread server
			// events, which surfaces as ECONNRESET rather than EOF. Frame
			// destruction remains mandatory in both cases.
			if (errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)) && sawCapture {
				if frameID != 0 {
					return fmt.Errorf("client closed with screencopy frame %d still alive", frameID)
				}
				return nil
			}
			return fmt.Errorf("read Wayland request: %w", err)
		}
		size := int(wl.Uint32(header[4:8]) >> 16)
		if size < 8 || size > 8192 {
			return fmt.Errorf("invalid Wayland request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender := wl.Uint32(header[:4])
		opcode := wl.Uint32(header[4:8]) & 0xffff
		if sender == 1 && opcode == 1 { // get registry
			registryID = wl.Uint32(body[:4])
			continue
		}
		if sender == 1 && opcode == 0 { // sync callback completes after advertised globals
			callback := wl.Uint32(body[:4])
			if managerID == 0 {
				for _, ev := range []struct {
					name    uint32
					iface   string
					version uint32
				}{
					{41, "zwlr_screencopy_manager_v1", 3}, {42, "wl_shm", 1}, {43, "wl_output", map[bool]uint32{true: 1, false: 4}[frameOutcome == "output v1"]},
				} {
					if err := writeExtTestEvent(conn, registryID, 0, wlrGlobalPayload(ev.name, ev.iface, ev.version)); err != nil {
						return err
					}
				}
			} else if outputID != 0 {
				if frameOutcome == "disconnect before capture request" {
					if err := writeExtTestEvent(conn, callback, 0, wlUint32Payload(0)); err != nil {
						return err
					}
					return nil
				}
				if frameOutcome != "omit output mode" {
					if err := writeExtTestEvent(conn, outputID, 1, wlUint32Payload(0, 2, 2, 60000)); err != nil {
						return err
					}
				}
				// An output can advertise geometry without ever reporting its
				// optional scale. Keep this behavior available to synthetic
				// capture tests of the unknown-scale fallback.
				if frameOutcome != "omit output scale" {
					if err := writeExtTestEvent(conn, outputID, 3, wlUint32Payload(1)); err != nil {
						return err
					}
				}
			}
			if err := writeExtTestEvent(conn, callback, 0, wlUint32Payload(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registryID && opcode == 0 {
			name := wl.Uint32(body[:4])
			if name == 43 && frameOutcome == "output v1" {
				requestedVersion := wl.Uint32(body[len(body)-8 : len(body)-4])
				if requestedVersion != 1 {
					return fmt.Errorf("wl_output v1 compositor rejected requested bind version %d", requestedVersion)
				}
			}
			newID := wl.Uint32(body[len(body)-4:])
			switch name {
			case 41:
				managerID = newID
			case 43:
				outputID = newID
			}
			continue
		}
		if sender == managerID && opcode == 0 {
			if frameID != 0 {
				return fmt.Errorf("new capture started before frame %d was destroyed", frameID)
			}
			frameCount++
			frameID = wl.Uint32(body[:4])
			sawCapture = true
			if frameOutcome == "disconnect after capture request" {
				return nil
			}
			if frameOutcome == "failed before buffer" {
				if err := writeExtTestEvent(conn, frameID, 3, nil); err != nil {
					return err
				}
				continue
			}
			if frameOutcome == "invalid buffer" {
				if err := writeExtTestEvent(conn, frameID, 0, []byte{0, 0, 0, 0}); err != nil {
					return err
				}
				continue
			}
			if frameOutcome == "ready before buffer" {
				if err := writeExtTestEvent(conn, frameID, 2, wlUint32Payload(0, 0, 0)); err != nil {
					return err
				}
				// The protocol is already invalid. Await frame destruction
				// rather than racing a second event against client shutdown.
				continue
			}
			buffer := wlUint32Payload(0, 2, 2, 8)
			switch frameOutcome {
			case "undersized stride":
				buffer = wlUint32Payload(0, 2, 2, 4)
			case "zero-height buffer":
				buffer = wlUint32Payload(0, 2, 0, 8)
			case "oversized framebuffer":
				buffer = wlUint32Payload(0, 2, 2, ^uint32(0))
			}
			if frameOutcome == "geometry change after first" && frameCount == 2 {
				buffer = wlUint32Payload(0, 3, 2, 12)
			}
			if err := writeExtTestEvent(conn, frameID, 0, buffer); err != nil {
				return err
			}
			if frameOutcome == "disconnect before pre-copy round-trip" {
				return nil
			}
			if frameOutcome == "premature ready" {
				if err := writeExtTestEvent(conn, frameID, 2, wlUint32Payload(0, 0, 0)); err != nil {
					return err
				}
			}
			continue
		}
		if frameID != 0 && sender == frameID && opcode == 1 { // destroy frame
			frameID = 0
			if frameOutcome == "cancel after copy request" && sawCopy {
				return nil
			}
			continue
		}
		if frameID != 0 && sender == frameID && opcode == 0 { // copy
			sawCopy = true
			if frameOutcome == "cancel after copy request" && (copyObserved == nil || postCopyEventPermit == nil) {
				return errors.New("post-copy cancellation fixture requires event synchronization channels")
			}
			if frameOutcome == "cancel after copy request" {
				copyObserved <- struct{}{}
				<-postCopyEventPermit
				// An output-scale update is valid during an active capture and
				// wakes dispatch without completing the frame.
				if err := writeExtTestEvent(conn, outputID, 3, wlUint32Payload(2)); err != nil {
					return err
				}
				continue
			}
			if frameOutcome == "disconnect after copy request" {
				return nil
			}
			if frameOutcome == "premature ready" || frameOutcome == "ready before buffer" {
				continue // no valid post-copy frame is ever made available
			}
			if frameOutcome == "failed after copy" {
				if err := writeExtTestEvent(conn, frameID, 3, nil); err != nil {
					return err
				}
				continue
			}
			if err := writeExtTestEvent(conn, frameID, 2, wlUint32Payload(0, 0, 0)); err != nil {
				return err
			}
			continue
		}
		_ = sawCopy
	}
}

func TestWlrScreencopySyntheticProtocolCaptureAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		outcome     string
		op          func(*WlrScreencopyBackend) (image.Image, uint32, error)
		wantErr     bool
		wantErrText string
		wantNoHash  bool
		rect        image.Rectangle
	}{
		{name: "output version one", outcome: "output v1", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "full image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "repeated full image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			if _, err := b.GrabFullHash(context.Background()); err != nil {
				return nil, 0, err
			}
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "cropped image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
			return img, 0, err
		}},
		{name: "full hash", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}},
		{name: "region hash", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return nil, h, err
		}, rect: image.Rect(0, 0, 1, 1)},
		{name: "region outside framebuffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(500, 500, 510, 510))
			return nil, h, err
		}, wantErr: true},
		{name: "failed before buffer", outcome: "failed before buffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, wantErr: true},
		{name: "failed after copy", outcome: "failed after copy", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}, wantErr: true},
		{name: "invalid buffer", outcome: "invalid buffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, wantErr: true},
		{name: "disconnect before pre-copy round-trip", outcome: "disconnect before pre-copy round-trip", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, wantErr: true, wantErrText: "pre-copy round-trip"},
		{name: "disconnect before capture request", outcome: "disconnect before capture request", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			hash, err := b.GrabFullHash(context.Background())
			return nil, hash, err
		}, wantErr: true, wantErrText: "capture_output", wantNoHash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
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
				serverDone <- wlrFakeCompositor(conn, tc.outcome)
			}()
			b := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
			img, hash, captureErr := tc.op(b)
			if tc.wantErr {
				if captureErr == nil || img != nil {
					t.Fatalf("compositor failure accepted: image=%v err=%v", img, captureErr)
				}
				if tc.wantNoHash && hash != 0 {
					t.Fatalf("failed capture published hash %08x", hash)
				}
				if tc.name == "region outside framebuffer" {
					if !errors.Is(captureErr, find.ErrEmptyRegion) {
						t.Fatalf("invalid region did not expose ErrEmptyRegion: %v", captureErr)
					}
				} else if tc.outcome == "disconnect before capture request" {
					// The peer closes immediately after completing the registry
					// round-trip. Depending on socket scheduling, the next read
					// may discover the disconnect during dispatch, before the
					// capture_output request can be attempted. Both are valid
					// fail-closed transport outcomes; no hash may be published.
					if !strings.Contains(captureErr.Error(), "capture_output") && !strings.Contains(captureErr.Error(), "dispatch:") {
						t.Fatalf("unexpected pre-capture disconnect error: %v", captureErr)
					}
				} else if tc.wantErrText != "" && !strings.Contains(captureErr.Error(), tc.wantErrText) {
					t.Fatalf("capture error = %v, want %q", captureErr, tc.wantErrText)
				} else if tc.wantErrText == "" && !strings.Contains(captureErr.Error(), "failed") {
					t.Fatalf("failure did not surface compositor error: %v", captureErr)
				}
			} else {
				if captureErr != nil {
					t.Fatalf("wire capture failed: %v", captureErr)
				}
				if strings.Contains(tc.name, "image") || tc.name == "output version one" {
					want := image.Pt(2, 2)
					if tc.name == "cropped image" {
						want = image.Pt(1, 1)
					}
					if img == nil || img.Bounds().Size() != want {
						t.Fatalf("wrong image: %v want size %v", img, want)
					}
				} else {
					want, e := find.PixelHashBGRA(make([]byte, 16), 2, 2, 8, tc.rect)
					if e != nil {
						t.Fatal(e)
					}
					if hash != want {
						t.Fatalf("hash=%08x want=%08x", hash, want)
					}
				}
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic Wayland server: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("compositor did not terminate")
			}
		})
	}
}

// A partially constructed screencopy backend is not an authorized compositor
// connection. Public capture methods must return errors, not panic or spawn
// a janitor against uninitialized state.
func TestWlrUninitializedBackendFailsClosedWithoutPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*WlrScreencopyBackend) error
	}{
		{name: "full screenshot", run: func(b *WlrScreencopyBackend) error {
			_, err := b.Grab(context.Background(), image.Rectangle{})
			return err
		}},
		{name: "full hash", run: func(b *WlrScreencopyBackend) error { _, err := b.GrabFullHash(context.Background()); return err }},
		{name: "region hash", run: func(b *WlrScreencopyBackend) error {
			_, err := b.GrabRegionHash(context.Background(), image.Rect(1, 2, 3, 4))
			return err
		}},
		{name: "resolution", run: func(b *WlrScreencopyBackend) error {
			_, _, err := b.ResolutionWithContext(context.Background())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			err := tc.run(b)
			if err == nil || !strings.Contains(err.Error(), "uninitialized") {
				t.Fatalf("uninitialized capture error = %v", err)
			}
			if closeErr := b.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}

// TestWithWlrContextReconnect simulates a protocol error on the first context
// and ensures withWlrContext resets the cached ctx and reconnects on next call.
func TestWithWlrContextReconnect(t *testing.T) {
	call := 0

	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-reconnect", func(sock string) (*wl.Context, error) {
		// first call returns a context that will cause fn to return error
		if call == 0 {
			call++
			return &wl.Context{}, nil
		}
		// subsequent calls succeed
		call++
		return &wl.Context{}, nil
	}, 5*time.Minute)
	defer b.Close()

	// First withWlrContext: simulate fn returning an error (protocol error)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return fmt.Errorf("simulated protocol error") }); err == nil {
		t.Fatalf("expected simulated protocol error")
	}

	// Ensure ctx was reset
	if b.ctx != nil {
		t.Fatalf("expected cached ctx to be nil after error")
	}

	// Next call should reconnect
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("expected reconnect to succeed, got %v", err)
	}
}

// A nonzero empty rect is malformed, not the full-output sentinel. It must
// fail before capture, rather than silently expose a full desktop frame.
func TestWlrScreencopyRejectsNonzeroEmptyRegionsBeforeCapture(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) returned image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) returned hash=%08x err=%v; want invalid region", rect, hash, err)
			}
		})
	}
}

func TestWlrResolutionReturnsPhysicalDimensions(t *testing.T) {
	b := &WlrScreencopyBackend{pW: 1920, pH: 1080, scale: 2}
	w, h, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution() error = %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Fatalf("Resolution() = %dx%d, want 1920x1080", w, h)
	}
}

func TestWlrResolutionIgnoresScaleOneAndZero(t *testing.T) {
	tests := []struct {
		name  string
		scale uint32
	}{
		{name: "scale zero", scale: 0},
		{name: "scale one", scale: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{pW: 1366, pH: 768, scale: tt.scale}
			w, h, err := b.Resolution()
			if err != nil {
				t.Fatalf("Resolution() error = %v", err)
			}
			if w != 1366 || h != 768 {
				t.Fatalf("Resolution() = %dx%d, want 1366x768", w, h)
			}
		})
	}
}

func TestWlrFrameDestroyRequest(t *testing.T) {
	ctx := &extCaptureTestCtx{}
	if err := wlSendFrameDestroy(context.Background(), ctx, 42); err != nil {
		t.Fatalf("wlSendFrameDestroy: %v", err)
	}
	if len(ctx.msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(ctx.msgs))
	}
	if got := wl.Uint32(ctx.msgs[0][0:4]); got != 42 {
		t.Fatalf("sender = %d, want 42", got)
	}
	if got := wl.Uint32(ctx.msgs[0][4:8]); got != 8<<16|1 {
		t.Fatalf("destroy request header = %#x, want %#x", got, uint32(8<<16|1))
	}
}

func TestWithWlrContextCachingAndReset(t *testing.T) {
	// Create backend with fake connector
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 5*time.Minute)
	defer b.Close()
	// first call should create a context
	var firstPtr, secondPtr *wl.Context
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		firstPtr = ctx
		return nil
	}); err != nil {
		t.Fatalf("first withWlrContext failed: %v", err)
	}

	// second call should reuse same pointer
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		secondPtr = ctx
		return nil
	}); err != nil {
		t.Fatalf("second withWlrContext failed: %v", err)
	}

	if firstPtr != secondPtr {
		t.Fatalf("expected same ctx pointer, got different: %p vs %p", firstPtr, secondPtr)
	}

	// simulate failure during fn; cached context should be closed and reset
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		return fmt.Errorf("simulated")
	}); err == nil {
		t.Fatalf("expected error from simulated fn")
	}

	if b.ctx != nil {
		t.Fatalf("expected cached ctx to be nil after error, got %v", b.ctx)
	}
}

func TestWlrCacheJanitorEvicts(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock-evict", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 50*time.Millisecond)
	defer b.Close()
	// create context
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("setup withWlrContext failed: %v", err)
	}

	// mark lastUsed as old
	b.ctxMu.Lock()
	if b.ctx != nil {
		b.lastUsed = time.Now().Add(-time.Hour)
	}
	b.ctxMu.Unlock()

	// wait for janitor to run
	time.Sleep(150 * time.Millisecond)

	b.ctxMu.Lock()
	exists := b.ctx != nil
	b.ctxMu.Unlock()
	if exists {
		t.Fatalf("expected cache entry to be evicted by janitor")
	}
}

func TestWlrScreencopyBackendCloseIsIdempotent(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock-close", func(sock string) (*wl.Context, error) {
		return &wl.Context{}, nil
	}, time.Minute)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("setup withWlrContext failed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

func TestWlrScreencopyBackendDiagnosticsReportSerializationAndHealth(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("fake-diagnostics", nil, time.Minute)
	t.Cleanup(func() { _ = b.Close() })
	diagnostics := strings.Join(b.Diagnostics(), "\n")
	for _, want := range []string{"serialized", "active cancellation", "no recorded failures"} {
		if !strings.Contains(diagnostics, want) {
			t.Fatalf("diagnostics %q missing %q", diagnostics, want)
		}
	}
}

func TestWlrScreencopyBackendRejectsOperationsAfterClose(t *testing.T) {
	connectCalls := 0
	b := NewWlrScreencopyBackendWithConnector(
		"fake-after-close",
		func(string) (*wl.Context, error) {
			connectCalls++
			return &wl.Context{}, nil
		},
		time.Minute,
	)
	if err := b.withWlrContext(func(*wl.Context) error { return nil }); err != nil {
		t.Fatalf("initial withWlrContext: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.withWlrContext(func(*wl.Context) error { return nil }); err == nil {
		t.Fatal("withWlrContext succeeded after Close")
	}
	if connectCalls != 1 {
		t.Fatalf("connector calls = %d, want 1 after close", connectCalls)
	}
}

func TestWlrCloseMarksBackendClosedBeforeWaitingForContext(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("fake-close-order", func(string) (*wl.Context, error) {
		return &wl.Context{}, nil
	}, time.Minute)

	b.ctxMu.Lock()
	released := false
	defer func() {
		if !released {
			b.ctxMu.Unlock()
		}
	}()

	closeDone := make(chan struct{})
	go func() {
		_ = b.Close()
		close(closeDone)
	}()

	select {
	case <-b.done:
	case <-time.After(time.Second):
		t.Fatal("Close did not begin")
	}
	if !b.closed.Load() {
		t.Fatal("backend was not marked closed before resource teardown")
	}

	b.ctxMu.Unlock()
	released = true
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after context mutex was released")
	}
}

// wl_output.mode carries flags before width and height. Reading the size one
// field late reported Resolution as (height, refresh), which then scaled every
// capture region wrongly.
func TestWlrModeEventDecodesSizeInProtocolOrder(t *testing.T) {
	data := make([]byte, 16)
	wl.PutUint32(data[0:4], 1)       // flags
	wl.PutUint32(data[4:8], 3840)    // width
	wl.PutUint32(data[8:12], 2160)   // height
	wl.PutUint32(data[12:16], 60000) // refresh

	mode, ok := wl.DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected the payload")
	}
	b := &WlrScreencopyBackend{pW: int(mode.Width), pH: int(mode.Height), scale: 1}

	width, height, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if width != 3840 || height != 2160 {
		t.Fatalf("Resolution = %dx%d, want 3840x2160", width, height)
	}
}

// A truncated mode payload must leave the previous extent untouched rather than
// half-applying a geometry change.
func TestWlrModeEventRejectsTruncatedPayload(t *testing.T) {
	if _, ok := wl.DecodeOutputMode(make([]byte, 12)); ok {
		t.Fatal("DecodeOutputMode accepted a 12-byte payload")
	}
	b := &WlrScreencopyBackend{pW: 1920, pH: 1080, scale: 1}
	width, height, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if width != 1920 || height != 1080 {
		t.Fatalf("Resolution = %dx%d, want the unchanged 1920x1080", width, height)
	}
}
