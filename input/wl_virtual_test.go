package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The virtual Wayland backend must revoke an operation waiting behind a
// currently admitted operation. A revoked request must never acquire the
// shared display and inject messages after Close starts.
func TestWlVirtualCloseRevokesQueuedInputWithoutDispatch(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- b.withOperation(context.Background(), func(_ wl.Ctx, ctx context.Context) error {
			close(firstEntered)
			<-ctx.Done()
			<-releaseFirst
			return ctx.Err()
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first operation did not enter display")
	}
	queued := make(chan error, 1)
	dispatched := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(context.Background(), func(wl.Ctx, context.Context) error { dispatched <- struct{}{}; return nil })
	}()
	// Ensure the second caller has been admitted before initiating Close. A
	// preempted caller is allowed to be rejected at admission instead.
	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) && (err == nil || err.Error() != "input/wl-virtual: backend not initialised") && (err == nil || err.Error() != "input/wl-virtual: backend is closed") {
			t.Fatalf("queued request unexpectedly completed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued operation failed to drain")
	}
	select {
	case <-dispatched:
		t.Fatal("queued operation dispatched after close")
	default:
	}
	close(releaseFirst)
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active operation ignored shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active operation did not return")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked after both operations drained")
	}
	if msgs := readCapturedWlWrites(t, conn); len(msgs) != 0 {
		t.Fatalf("revoked operations emitted %d Wayland frames", len(msgs))
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

// A compositor that cannot report pointer location may return Unsupported
// while active, but must report its revoked backend state after Close. In
// particular, closing cannot reauthorize any pointer query through a stale
// virtual-device handle or leak previously observed pointer coordinates.
func TestWlVirtualPointerLocationRejectsQueriesAfterShutdown(t *testing.T) {
	var absent *WlVirtualBackend
	if x, y, err := absent.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("nil backend pointer result=%d,%d err=%v", x, y, err)
	}
	b, conn := newCapturedWlVirtualBackend(t)
	if x, y, err := b.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("active unsupported pointer result=%d,%d err=%v", x, y, err)
	}
	if err := b.checkOpen(); err != nil {
		t.Fatalf("live backend was marked closed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		x, y, err := b.PointerLocation(context.Background())
		if x != 0 || y != 0 || err == nil || !strings.Contains(err.Error(), "backend is closed") || errors.Is(err, ErrNotSupported) {
			t.Fatalf("closed backend returned pointer data/unsupported: %d,%d %v", x, y, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if x, y, err := b.PointerLocation(ctx); x != 0 || y != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled closed query result=%d,%d err=%v", x, y, err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if messages := readCapturedWlWrites(t, conn); len(messages) != 0 {
		t.Fatalf("pointer queries leaked %d protocol messages", len(messages))
	}
}

func virtualGlobalPayload(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	buf := make([]byte, 8+pad+4)
	wl.PutUint32(buf, name)
	wl.PutUint32(buf[4:], uint32(n))
	copy(buf[8:], iface)
	wl.PutUint32(buf[8+pad:], version)
	return buf
}
func virtualEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	if len(body)%4 != 0 {
		return fmt.Errorf("invalid synthetic event alignment: %d", len(body))
	}
	pkt := make([]byte, 8+len(body))
	wl.PutUint32(pkt[:4], sender)
	wl.PutUint32(pkt[4:8], uint32(len(pkt))<<16|opcode)
	copy(pkt[8:], body)
	_, err := conn.Write(pkt)
	return err
}
func virtualWords(words ...uint32) []byte {
	buf := make([]byte, 4*len(words))
	for i, w := range words {
		wl.PutUint32(buf[4*i:], w)
	}
	return buf
}

func virtualManagerCompositor(conn *net.UnixConn, advertised map[string]uint32, wantReady bool, failOutputRoundTrip ...bool) error {
	failOutput := len(failOutputRoundTrip) != 0 && failOutputRoundTrip[0]
	return virtualManagerCompositorWithKeyboardCreate(conn, advertised, wantReady, failOutput, nil)
}

func virtualManagerCompositorWithKeyboardCreate(conn *net.UnixConn, advertised map[string]uint32, wantReady, failOutputRoundTrip bool, afterKeyboardCreate func()) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registry, output, ptrMgr, kbdMgr uint32
	synced := 0
	var pointer, keyboard uint32
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("Wayland fake read: %w", err)
		}
		size := int(wl.Uint32(h[4:8]) >> 16)
		if size < 8 || size > 100000 {
			return fmt.Errorf("bad wire size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:8])&0xffff
		if sender == 1 && opcode == 1 {
			registry = wl.Uint32(body[:4])
			continue
		}
		if sender == 1 && opcode == 0 {
			cb := wl.Uint32(body[:4])
			synced++
			if synced == 1 {
				for name, id := range map[string]uint32{"zwlr_virtual_pointer_manager_v1": 11, "zwp_virtual_keyboard_manager_v1": 12, "wl_seat": 13, "wl_output": 14} {
					if version := advertised[name]; version != 0 {
						if err := virtualEvent(conn, registry, 0, virtualGlobalPayload(id, name, version)); err != nil {
							return err
						}
					}
				}
			} else if output != 0 {
				if failOutputRoundTrip {
					return nil
				}
				// A real wl_output advertises alternative modes after the active mode.
				// The alternate cannot replace the pointer's active coordinate space.
				if err := virtualEvent(conn, output, 1, virtualWords(wl.OutputModeCurrent, 800, 600, 60000)); err != nil {
					return err
				}
				if err := virtualEvent(conn, output, 1, virtualWords(0, 1280, 720, 60000)); err != nil {
					return err
				}
				if err := virtualEvent(conn, output, 3, virtualWords(2)); err != nil {
					return err
				}
			}
			if err := virtualEvent(conn, cb, 0, virtualWords(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registry && opcode == 0 && len(body) >= 8 {
			name := wl.Uint32(body[:4])
			id := wl.Uint32(body[len(body)-4:])
			switch name {
			case 11:
				ptrMgr = id
			case 12:
				kbdMgr = id
			}
			if name == 14 {
				output = id
				ver := wl.Uint32(body[len(body)-8 : len(body)-4])
				if ver > advertised["wl_output"] {
					return fmt.Errorf("invalid wl_output bind version %d", ver)
				}
			}
			continue
		}
		// A real manager must create both pointer and keyboard objects, then send
		// at least one keyboard warmup event before reporting the backend ready.
		if size >= 16 && opcode == 0 {
			if sender == ptrMgr {
				pointer = wl.Uint32(body[len(body)-4:])
			}
			if sender == kbdMgr {
				keyboard = wl.Uint32(body[len(body)-4:])
				if afterKeyboardCreate != nil {
					afterKeyboardCreate()
					return nil
				}
			}
		}
		if wantReady && pointer != 0 && keyboard != 0 && sender == keyboard && opcode == 1 {
			// The constructor warms up with both key down and key up. Keep
			// reading until Close so no successful write loses the transport.
			wantReady = false
		}
	}
}

func TestWlVirtualConstructorRejectsMissingAdvertisedProtocols(t *testing.T) {
	full := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	for _, missing := range []string{"zwlr_virtual_pointer_manager_v1", "zwp_virtual_keyboard_manager_v1", "wl_seat"} {
		t.Run(missing, func(t *testing.T) {
			globals := make(map[string]uint32, len(full))
			for k, v := range full {
				if k != missing {
					globals[k] = v
				}
			}
			sock := shortPrivateSocketPath(t, "wl.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, lintShadow151 := listener.AcceptUnix()
				if lintShadow151 != nil {
					serverDone <- lintShadow151
					return
				}
				serverDone <- virtualManagerCompositor(conn, globals, false)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := NewWlVirtualBackendContext(ctx, sock)
			if b != nil || err == nil || !strings.Contains(err.Error(), "advertised") {
				t.Fatalf("missing protocol %q was accepted: %v %v", missing, b, err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("backend did not close rejected connection")
			}
		})
	}
}

func TestWlVirtualConstructorUsesRealAdvertisedGlobalsAndInputWire(t *testing.T) {
	globals := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	socket := shortPrivateSocketPath(t, "wl.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, lintShadow186 := listener.AcceptUnix()
		if lintShadow186 != nil {
			serverDone <- lintShadow186
			return
		}
		serverDone <- virtualManagerCompositor(conn, globals, true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := NewWlVirtualBackendContext(ctx, socket)
	if err != nil {
		t.Fatalf("valid synthetic Wayland compositor rejected: %v", err)
	}
	defer b.Close()
	width, height := b.outputExtent()
	if width != 400 || height != 300 {
		t.Fatalf("physical 800x600 at 2x scale must give 400x300, got %dx%d", width, height)
	}
	if err := b.MouseMove(ctx, 140, 120); err != nil {
		t.Fatalf("virtual pointer failed after initialization: %v", err)
	}
	if err := b.Type(ctx, "Ab"); err != nil {
		t.Fatalf("virtual keyboard failed after initialization: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("compositor reported bad wire sequence: %v", serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compositor did not observe backend cleanup")
	}
}

func TestWlVirtualConstructorReportsOutputRoundTripFailure(t *testing.T) {
	globals := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	socket := shortPrivateSocketPath(t, "wl.sock")
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
		serverDone <- virtualManagerCompositor(conn, globals, false, true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := NewWlVirtualBackendContext(ctx, socket)
	if backend != nil || err == nil || !strings.Contains(err.Error(), "output round-trip") {
		t.Fatalf("failed wl_output round-trip was not reported: backend=%v err=%v", backend, err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatal("backend did not close failed output connection")
	}
}

func TestWlVirtualConstructorPropagatesCancellationDuringKeyboardWarmup(t *testing.T) {
	globals := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	socket := shortPrivateSocketPath(t, "wl.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- virtualManagerCompositorWithKeyboardCreate(conn, globals, false, false, cancel)
	}()

	backend, err := NewWlVirtualBackendContext(ctx, socket)
	if backend != nil || !errors.Is(err, context.Canceled) || !strings.Contains(fmt.Sprint(err), "keyboard warmup") {
		t.Fatalf("canceled keyboard warmup returned backend=%v err=%v", backend, err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("constructor did not close the canceled keyboard session")
	}
}

// The logical extent is derived from the reported physical size and scale, and
// a scale event that arrives before any mode event must not divide by zero.
func TestOutputGeometryDerivesLogicalExtent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		apply      func(*WlVirtualBackend)
		wantWidth  uint32
		wantHeight uint32
	}{
		{
			name:       "unknown until a mode event arrives",
			apply:      func(*WlVirtualBackend) {},
			wantWidth:  0,
			wantHeight: 0,
		},
		{
			name:       "mode event at scale one",
			apply:      func(b *WlVirtualBackend) { b.applyOutputMode(3840, 2160) },
			wantWidth:  3840,
			wantHeight: 2160,
		},
		{
			name: "scaled logical extent",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputMode(3840, 2160)
				b.applyOutputScale(2)
			},
			wantWidth:  1920,
			wantHeight: 1080,
		},
		{
			name: "rescaling recomputes from the physical size",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputMode(3840, 2160)
				b.applyOutputScale(2)
				b.applyOutputScale(4)
			},
			wantWidth:  960,
			wantHeight: 540,
		},
		{
			name:       "scale alone supplies no geometry",
			apply:      func(b *WlVirtualBackend) { b.applyOutputScale(0) },
			wantWidth:  0,
			wantHeight: 0,
		},
		{
			name: "scale before any mode keeps the known extent",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputScale(2)
				b.applyOutputMode(3840, 2160)
			},
			wantWidth:  1920,
			wantHeight: 1080,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlVirtualBackend{}
			tc.apply(b)

			width, height := b.outputExtent()
			if width != tc.wantWidth || height != tc.wantHeight {
				t.Fatalf("extent = %dx%d, want %dx%d", width, height, tc.wantWidth, tc.wantHeight)
			}
		})
	}
}

// wl_output events arrive on the shared session's dispatch goroutine while
// input operations read the extent to validate and encode motion_absolute
// events, so both sides must be synchronized and a reader must never observe a
// half-applied geometry change.
//
// Every goroutine is parked on a start barrier and released together, so the
// readers and the writer provably overlap instead of depending on scheduling.
func TestOutputExtentIsSafeDuringConcurrentGeometryChanges(t *testing.T) {
	const (
		readers    = 4
		iterations = 20000
	)

	b := &WlVirtualBackend{}
	b.applyOutputMode(3840, 2160)

	var ready, done sync.WaitGroup
	start := make(chan struct{})

	for range readers {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			for range iterations {
				width, height := b.outputExtent()
				// Every extent this backend derives comes from a 3840x2160 mode
				// at scale 1 to 4, so a reader must never see a torn pair: a
				// zero edge, or a width and height from different updates.
				if width == 0 || height == 0 {
					t.Errorf("observed a collapsed extent %dx%d", width, height)
					return
				}
				if width*9 != height*16 {
					t.Errorf("observed a torn extent %dx%d for a 16:9 mode", width, height)
					return
				}
			}
		}()
	}

	ready.Add(1)
	done.Add(1)
	go func() {
		defer done.Done()
		ready.Done()
		<-start
		for i := range iterations {
			b.applyOutputMode(3840, 2160)
			b.applyOutputScale(uint32(i%4) + 1)
		}
	}()

	ready.Wait()
	close(start)
	done.Wait()
}

// The wl_output.mode event carries flags before width and height. Reading the
// size one field late took the height and the refresh rate as the output size,
// which then became the extent every absolute pointer coordinate was validated
// and encoded against.
func TestWlVirtualModeEventDecodesSizeInProtocolOrder(t *testing.T) {
	data := make([]byte, 16)
	wl.PutUint32(data[0:4], 1)       // flags
	wl.PutUint32(data[4:8], 3840)    // width
	wl.PutUint32(data[8:12], 2160)   // height
	wl.PutUint32(data[12:16], 60000) // refresh

	mode, ok := wl.DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected the payload")
	}
	b := &WlVirtualBackend{}
	b.applyOutputMode(mode.Width, mode.Height)

	width, height := b.outputExtent()
	if width != 3840 || height != 2160 {
		t.Fatalf("extent = %dx%d, want 3840x2160 (height and refresh must not be read as the size)", width, height)
	}
}

// Without wl_output mode, a guessed 1920x1080 frame can dispatch wrong
// absolute pointer coordinates on smaller or scaled displays. No pointer
// motion may be sent until the compositor publishes actual geometry.
func TestWlVirtualPointerRejectsUnknownOutputGeometry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint32
	}{
		{"unreported", 0, 0}, {"missing width", 0, 1080}, {"missing height", 1920, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newCapturedWlVirtualBackend(t)
			b.extentMu.Lock()
			b.outW, b.outH = tc.width, tc.height
			b.extentMu.Unlock()
			err := b.MouseMove(context.Background(), 3, 5)
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unknown output extent allowed motion: %v", err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if writes := readCapturedWlWrites(t, conn); len(writes) != 0 {
				t.Fatalf("unexpected pointer messages: %d", len(writes))
			}
		})
	}
}

// Scroll entry points validate public notation, but the backend's shared
// protocol encoder must also enforce its signed 24.8 representation limit.
// In particular, a negative overflow must fail before dereferencing an
// uninitialized compositor transport or sending any wire messages.
func TestWlVirtualScrollEncoderRejectsSignedOverflowBeforeTransport(t *testing.T) {
	b := &WlVirtualBackend{}
	for _, clicks := range []int{-maxScrollClicks - 1, maxScrollClicks + 1} {
		err := b.scroll(context.Background(), 0, clicks)
		if err == nil || !strings.Contains(err.Error(), "scroll clicks must be at most") {
			t.Fatalf("unbounded scroll %d reached transport: %v", clicks, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.scroll(ctx, 0, maxScrollClicks+1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scroll dispatched or lost cancellation precedence: %v", err)
	}
}

// Literal keyboard injection has to pass the same operation-admission and
// all-or-nothing text validation as the parsed Type method. Invalid trailing
// UTF-8 cannot inject the valid prefix or alter the keyboard's held state.
func TestWlVirtualLiteralTypingRejectsMalformedTextAndRevokedOperations(t *testing.T) {
	b, peer := newCapturedWlVirtualBackend(t)
	keyboard, wire := newTestKeyboard()
	b.kbd = keyboard
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.TypeLiteral(ctx, "authorized text"); !errors.Is(err, context.Canceled) {
		t.Fatalf("revoked caller injected literal text: %v", err)
	}
	malformed := "valid prefix" + string([]byte{0xff})
	if err := b.TypeLiteral(context.Background(), malformed); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed text was not rejected: %v", err)
	}
	if len(wire.msgs) != 0 || len(keyboard.held) != 0 || keyboard.mods != 0 {
		t.Fatalf("rejected text injected %d events or modified keys: held=%v mods=%d", len(wire.msgs), keyboard.held, keyboard.mods)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.TypeLiteral(context.Background(), "post-close text"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed backend accepted keyboard request: %v", err)
	}
	if len(wire.msgs) != 0 || len(readCapturedWlWrites(t, peer)) != 0 {
		t.Fatalf("rejected literal text leaked virtual-keyboard protocol writes")
	}
}

// A previously admitted virtual-input backend must not panic or inject
// partial events if its synthetic compositor fails before a keyboard or
// pointer proxy becomes available. These are actual connected Wayland sockets.
func TestWlVirtualMissingProtocolObjectFailsClosedWithoutSendingFrames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable func(*WlVirtualBackend)
		call    func(*WlVirtualBackend) error
	}{
		{"literal keyboard", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.TypeLiteral(context.Background(), "Café") }},
		{"key syntax keyboard", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.Type(context.Background(), "hello{ctrl+a}") }},
		{"keyboard press", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.KeyDown(context.Background(), "ctrl") }},
		{"keyboard release", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.KeyUp(context.Background(), "ctrl") }},
		{"pointer motion", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseMove(context.Background(), 40, 50) }},
		{"pointer button press", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"pointer click", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseClick(context.Background(), 40, 50, 1) }},
		{"pointer scroll", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.ScrollDown(context.Background(), 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newCapturedWlVirtualBackend(t)
			tc.disable(b)
			defer func() {
				if issue := recover(); issue != nil {
					t.Errorf("missing proxy panicked: %v", issue)
				}
			}()
			err := tc.call(b)
			if !errors.Is(err, ErrNotSupported) {
				t.Errorf("missing device error=%v, want unsupported", err)
			}
			if err := b.Close(); err != nil {
				t.Error(err)
			}
			if wire := readCapturedWlWrites(t, conn); len(wire) != 0 {
				t.Errorf("missing device sent %d Wayland messages", len(wire))
			}
		})
	}
}

// wl_output.scale/width/height are signed 32-bit protocol integers. A
// malformed negative wire number cannot become an enormous valid extent,
// override a previously confirmed geometry, or authorize out-of-bounds input.
func TestWlVirtualRejectsNegativeSignedGeometryWithoutChangingLastValidExtent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*WlVirtualBackend)
	}{
		{"negative scale -1", func(b *WlVirtualBackend) { b.applyOutputScale(^uint32(0)) }},
		{"negative scale minimum", func(b *WlVirtualBackend) { b.applyOutputScale(1 << 31) }},
		{"negative width", func(b *WlVirtualBackend) { b.applyOutputMode(^uint32(0), 1080) }},
		{"negative height", func(b *WlVirtualBackend) { b.applyOutputMode(1920, ^uint32(0)) }},
		{"zero width", func(b *WlVirtualBackend) { b.applyOutputMode(0, 1080) }},
		{"zero height", func(b *WlVirtualBackend) { b.applyOutputMode(1920, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlVirtualBackend{}
			b.applyOutputMode(1920, 1080)
			b.applyOutputScale(2)
			tc.apply(b)
			w, h := b.outputExtent()
			if w != 960 || h != 540 {
				t.Fatalf("invalid signed Wayland geometry changed confirmed 960x540 extent to %dx%d", w, h)
			}
		})
	}
}

func TestWlVirtualUntrustedScaleCannotAuthorizeOutOfBoundsPointerMove(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	b.applyOutputMode(1920, 1080)
	b.applyOutputScale(^uint32(0))
	if err := b.MouseMove(context.Background(), 2048, 500); err == nil {
		t.Fatal("malformed Wayland scale authorized out-of-bounds pointer motion")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if writes := readCapturedWlWrites(t, conn); len(writes) > 0 {
		t.Fatalf("out-of-bounds motion sent protocol messages: %d", len(writes))
	}
}

func TestWlVirtualOutputModeWireUsesSignedDimensions(t *testing.T) {
	mode := make([]byte, 16)
	wl.PutUint32(mode[4:8], ^uint32(0))
	wl.PutUint32(mode[8:12], 1080)
	decoded, ok := wl.DecodeOutputMode(mode)
	if !ok {
		t.Fatal("could not parse synthetic Wayland mode")
	}
	b := &WlVirtualBackend{}
	b.applyOutputMode(1920, 1080)
	b.applyOutputMode(decoded.Width, decoded.Height)
	w, h := b.outputExtent()
	if w != 1920 || h != 1080 {
		t.Fatalf("signed protocol width corrupted extent: %dx%d", w, h)
	}
}

// Virtual scroll requests must retain direction and axis through signed 24.8
// Wayland fixed-point encoding, not merely report successful API calls.
func TestWlVirtualScrollDirectionsEmitSignedAxisAndFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scroll func(*WlVirtualBackend) error
		axis   uint32
		value  int32
	}{
		{"up", func(b *WlVirtualBackend) error { return b.ScrollUp(context.Background(), 2) }, 0, -2 * 15 * 256},
		{"down", func(b *WlVirtualBackend) error { return b.ScrollDown(context.Background(), 3) }, 0, 3 * 15 * 256},
		{"left", func(b *WlVirtualBackend) error { return b.ScrollLeft(context.Background(), 4) }, 1, -4 * 15 * 256},
		{"right", func(b *WlVirtualBackend) error { return b.ScrollRight(context.Background(), 5) }, 1, 5 * 15 * 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newCapturedWlVirtualBackend(t)
			if err := tc.scroll(backend); err != nil {
				t.Fatalf("scroll failed: %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			messages := readCapturedWlWrites(t, peer)
			if len(messages) != 2 {
				t.Fatalf("scroll emitted %d requests, want axis and frame", len(messages))
			}
			axis, frame := messages[0], messages[1]
			if len(axis) != 20 || wl.Uint32(axis[0:4]) != 7 || wl.Uint32(axis[4:8]) != 20<<16|3 || wl.Uint32(axis[12:16]) != tc.axis || int32(wl.Uint32(axis[16:20])) != tc.value {
				t.Fatalf("incorrect scroll axis wire frame: %v; want axis %d value %d", axis, tc.axis, tc.value)
			}
			if len(frame) != 8 || wl.Uint32(frame[0:4]) != 7 || wl.Uint32(frame[4:8]) != 8<<16|4 {
				t.Fatalf("missing virtual-pointer frame after scroll: %v", frame)
			}
		})
	}
}

func TestWlVirtualScrollInvalidMagnitudeNeverEmitsProtocol(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	for _, invoke := range []func() error{
		func() error { return backend.ScrollUp(context.Background(), -1) },
		func() error { return backend.ScrollLeft(context.Background(), maxScrollClicks+1) },
		func() error { return backend.ScrollRight(context.Background(), -1) },
		func() error { return backend.ScrollDown(context.Background(), maxScrollClicks+1) },
	} {
		if err := invoke(); err == nil {
			t.Fatal("invalid scroll unexpectedly succeeded")
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readCapturedWlWrites(t, peer); len(got) != 0 {
		t.Fatalf("invalid scroll emitted %d protocol frames", len(got))
	}
}

// Direct virtual-input calls are not permitted to dispatch to an uninitialized
// Wayland connection, and cancellation wins even before resource validation.
func TestWlVirtualUninitializedOperationsFailBeforeProtocolAccess(t *testing.T) {
	var backend WlVirtualBackend
	live := context.Background()
	canceled, cancel := context.WithCancel(live)
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		run  func(context.Context) error
		want string
	}{
		{"uninitialized move", live, func(ctx context.Context) error { return backend.MouseMove(ctx, 3, 5) }, "not initialised"},
		{"uninitialized button", live, func(ctx context.Context) error { return backend.button(ctx, btnLeft, 1) }, "not initialised"},
		{"uninitialized scroll", live, func(ctx context.Context) error { return backend.scroll(ctx, 0, 1) }, "not initialised"},
		{"canceled button", canceled, func(ctx context.Context) error { return backend.button(ctx, btnLeft, 1) }, ""},
		{"canceled scroll", canceled, func(ctx context.Context) error { return backend.scroll(ctx, 0, 1) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(tc.ctx)
			if tc.want == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled operation reached Wayland setup: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("uninitialized operation reached Wayland transport: %v", err)
			}
		})
	}
}

func TestWlVirtualNilBackendCloseAndUninitializedCoordinateSpace(t *testing.T) {
	var nilBackend *WlVirtualBackend
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if _, err := nilBackend.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("nil backend coordinate space was reported: %v", err)
	}
	var empty WlVirtualBackend
	space, err := empty.PointerCoordinateSpace(context.Background())
	if err != nil || space.Kind != CoordinateSpaceLogical {
		t.Fatalf("uninitialized coordinate space: %+v %v", space, err)
	}
}
