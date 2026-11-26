package wl

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A malformed or unrelated Wayland event must not complete a pending
// synchronization callback. Only wl_callback.done (opcode zero) may invoke it.
func TestCallbackDispatchOnlyDoneCompletesWaiter(t *testing.T) {
	var calls int
	callback := &Callback{}
	callback.SetDoneHandler(func() { calls++ })
	for _, opcode := range []uint32{1, 2, ^uint32(0)} {
		callback.Dispatch(opcode, -1, []byte{0xff, 0xff})
		if calls != 0 {
			t.Fatalf("unexpected opcode %d completed callback", opcode)
		}
	}
	callback.Dispatch(0, -1, nil)
	if calls != 1 {
		t.Fatalf("done callback calls = %d, want 1", calls)
	}
	callback.SetDoneHandler(nil)
	callback.Dispatch(0, -1, nil)
	if calls != 1 {
		t.Fatalf("removed done handler called again: %d", calls)
	}
}

// Shm, pool and buffer objects intentionally have no event handlers. Unknown
// compositor events must not change their identity or registry bookkeeping.
func TestSharedMemoryProxiesIgnoreUnrecognizedEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy Proxy
	}{
		{"shm", &Shm{}},
		{"pool", &ShmPool{}},
		{"buffer", &Buffer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{}
			tc.proxy.SetID(47)
			tc.proxy.SetCtx(ctx)
			for _, opcode := range []uint32{0, 1, ^uint32(0)} {
				tc.proxy.Dispatch(opcode, -1, []byte{1, 2, 3})
				if tc.proxy.ID() != 47 {
					t.Fatalf("unexpected event %d modified object identity", opcode)
				}
			}
		})
	}
}

func TestDispatchReportsDeadlineResetFailureAfterCallbackClosesConnection(t *testing.T) {
	for _, cancelInCallback := range []bool{false, true} {
		name := "closed"
		if cancelInCallback {
			name = "closed and canceled"
		}
		t.Run(name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client, err := ConnectContext(context.Background(), socket)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			peer, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			proxy := &RawProxy{OnEvent: func(_ uint32, _ int, _ []byte) {
				if err := client.Close(); err != nil {
					t.Errorf("close during event: %v", err)
				}
				if cancelInCallback {
					cancel()
				}
			}}
			client.Register(proxy)
			var event [8]byte
			PutUint32(event[:4], proxy.ID())
			PutUint32(event[4:], 8<<16)
			if _, err := peer.Write(event[:]); err != nil {
				t.Fatal(err)
			}
			err = client.DispatchContext(ctx)
			if !errors.Is(err, net.ErrClosed) || !strings.Contains(err.Error(), "reset read deadline") {
				t.Fatalf("closed connection reset error = %v", err)
			}
			if cancelInCallback && !errors.Is(err, context.Canceled) {
				t.Fatalf("reset failure lost caller cancellation: %v", err)
			}
		})
	}
}

func TestDispatchGrowsBufferForLargeWaylandEvent(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()
	ctx, err := ConnectContext(context.Background(), socket)
	if err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}
	peer, err := listener.AcceptUnix()
	if err != nil {
		_ = ctx.Close()
		t.Fatalf("AcceptUnix: %v", err)
	}
	t.Cleanup(func() {
		_ = ctx.Close()
		_ = peer.Close()
	})

	payload := bytes.Repeat([]byte{0x5a}, 5000)
	proxy := &RawProxy{}
	dispatched := make(chan struct {
		opcode uint32
		length int
	}, 1)
	proxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		dispatched <- struct {
			opcode uint32
			length int
		}{opcode: opcode, length: len(data)}
	}
	ctx.Register(proxy)

	message := make([]byte, 8+len(payload))
	PutUint32(message[0:4], proxy.ID())
	PutUint32(message[4:8], uint32(len(message))<<16|7)
	copy(message[8:], payload)
	if n, err := peer.Write(message); err != nil || n != len(message) {
		t.Fatalf("write large synthetic event: n=%d/%d err=%v", n, len(message), err)
	}
	if err := ctx.DispatchContext(context.Background()); err != nil {
		t.Fatalf("dispatch large synthetic event: %v", err)
	}
	select {
	case got := <-dispatched:
		if got.opcode != 7 || got.length != len(payload) {
			t.Fatalf("large event dispatch = opcode %d, payload %d; want opcode 7, payload %d", got.opcode, got.length, len(payload))
		}
	case <-time.After(time.Second):
		t.Fatal("large event did not reach its registered Wayland proxy")
	}
	if len(ctx.buf) < len(payload) {
		t.Fatalf("Wayland receive buffer remained %d bytes for %d-byte event", len(ctx.buf), len(payload))
	}
}

// Compositor-assigned new_id messages are untrusted. A malformed duplicate
// must not replace another live protocol proxy on the connection.
func TestContextSetProxyRejectsDuplicateOrNullObjectIdentities(t *testing.T) {
	ctx := &Context{}
	previous := &RawProxy{}
	ctx.Register(previous)
	replacement := &RawProxy{}
	ctx.SetProxy(previous.ID(), replacement)
	ctx.objectsMu.RLock()
	retained := ctx.objects[previous.ID()]
	ctx.objectsMu.RUnlock()
	if retained != previous {
		t.Fatalf("existing live proxy %d replaced by compositor-assigned new_id", previous.ID())
	}
	if replacement.ID() != 0 {
		t.Fatalf("rejected proxy inherited an existing protocol ID %d", replacement.ID())
	}
	zero := &RawProxy{}
	ctx.SetProxy(0, zero)
	ctx.objectsMu.RLock()
	_, registered := ctx.objects[0]
	ctx.objectsMu.RUnlock()
	if registered || zero.ID() != 0 {
		t.Fatal("reserved zero object identity registered")
	}
}

// A client-side allocation must not reuse an ID that was already registered
// as a compositor-created object, regardless of the order of events.
func TestContextRegisterSkipsExistingCompositorAssignedIdentity(t *testing.T) {
	ctx := &Context{}
	ctx.Register(&RawProxy{})
	prior := &RawProxy{}
	ctx.SetProxy(2, prior)
	next := &RawProxy{}
	ctx.Register(next)
	ctx.objectsMu.RLock()
	retained := ctx.objects[2]
	ctx.objectsMu.RUnlock()
	if retained != prior || next.ID() == 2 {
		t.Fatalf("client allocation clobbered server object 2: retained=%v next=%d", retained == prior, next.ID())
	}
}

// A server-provided object ID must not overwrite an existing proxy, even
// when client-side allocation wraps around the uint32 ID namespace.
func TestWaylandRegistryWraparoundPreservesExistingProxyIdentities(t *testing.T) {
	ctx := &Context{nextID: math.MaxUint32 - 1}
	high := &RawProxy{}
	ctx.Register(high)
	if high.ID() != math.MaxUint32 {
		t.Fatalf("highest client object ID=%d", high.ID())
	}
	low := &RawProxy{}
	ctx.Register(low)
	if low.ID() != 1 {
		t.Fatalf("wraparound object ID=%d, want 1", low.ID())
	}
	occupied := &RawProxy{}
	ctx.SetProxy(2, occupied)
	next := &RawProxy{}
	ctx.Register(next)
	if next.ID() != 3 {
		t.Fatalf("registration overwrote occupied object ID: got %d", next.ID())
	}
	intruder := &RawProxy{}
	ctx.SetProxy(high.ID(), intruder)
	if ctx.objects[high.ID()] != high || intruder.Ctx() != nil {
		t.Fatal("server-assigned ID replaced existing live proxy")
	}
	ctx.Unregister(intruder)
	if ctx.objects[high.ID()] != high {
		t.Fatal("unregistered unowned proxy evicted existing object")
	}
	ctx.Unregister(high)
	if _, found := ctx.objects[math.MaxUint32]; found {
		t.Fatal("destroyed proxy survived unregister")
	}
	if ctx.objects[low.ID()] != low || ctx.objects[occupied.ID()] != occupied || ctx.objects[next.ID()] != next {
		t.Fatal("registration or unregistration corrupted unrelated objects")
	}
}

// Stale teardown must not remove a later object that legitimately reused
// the same Wayland ID; nil and zero-ID requests have no registry authority.
func TestWaylandRegistryIgnoresStaleTeardownAndZeroIDs(t *testing.T) {
	ctx := &Context{}
	ctx.SetProxy(0, &RawProxy{})
	if len(ctx.objects) != 0 {
		t.Fatal("server registered reserved object ID zero")
	}
	var nilCtx *Context
	nilCtx.Unregister(&RawProxy{})
	ctx.Unregister(nil)
	Unregister(nil, &RawProxy{})
	Unregister(ctx, nil)
	old := &RawProxy{}
	ctx.SetProxy(9, old)
	newer := &RawProxy{}
	ctx.Unregister(old)
	ctx.SetProxy(9, newer)
	ctx.Unregister(old)
	if ctx.objects[9] != newer {
		t.Fatal("stale teardown evicted replacement object")
	}
	ctx.Unregister(newer)
	if _, exists := ctx.objects[9]; exists {
		t.Fatal("replacement teardown did not retire object")
	}
}

// Invalid shared-memory pool parameters must fail locally before a protocol
// object ID is consumed or a malformed request reaches the compositor.
func TestShmCreatePoolRejectsInvalidParametersBeforeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name string
		fd   int
		size int32
	}{
		{"negative FD", -1, 4096}, {"negative pool size", 0, -1}, {"zero pool size", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{nextID: 1}
			shm := &Shm{}
			shm.SetCtx(ctx)
			shm.SetID(12)
			pool, err := shm.CreatePoolContext(context.Background(), tc.fd, tc.size)
			if err == nil || pool != nil || len(ctx.objects) != 0 || ctx.nextID != 1 {
				t.Fatalf("invalid pool accepted or registered: pool=%p err=%v registry=%v nextID=%d", pool, err, ctx.objects, ctx.nextID)
			}
		})
	}
}

func TestShmCreateBufferRejectsInvalidGeometryBeforeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		offset, width, height, stride int32
	}{
		{"negative offset", -1, 2, 2, 8}, {"zero width", 0, 0, 2, 8},
		{"negative width", 0, -1, 2, 8}, {"zero height", 0, 2, 0, 8},
		{"negative height", 0, 2, -1, 8}, {"zero stride", 0, 2, 2, 0},
		{"negative stride", 0, 2, 2, -8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{nextID: 1}
			pool := &ShmPool{}
			pool.SetCtx(ctx)
			pool.SetID(14)
			buf, err := pool.CreateBufferContext(context.Background(), tc.offset, tc.width, tc.height, tc.stride, 0)
			if err == nil || buf != nil || len(ctx.objects) != 0 || ctx.nextID != 1 {
				t.Fatalf("invalid buffer accepted or registered: buf=%p err=%v registry=%v nextID=%d", buf, err, ctx.objects, ctx.nextID)
			}
		})
	}
}

func newShutdownTestConnection(t *testing.T) (*Context, *net.UnixConn) {
	t.Helper()
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	return client, peer
}

func TestRoundTripRejectsPeerThatCannotReceiveSyncAndUnregistersCallback(t *testing.T) {
	client, peer := newShutdownTestConnection(t)
	if err := peer.CloseRead(); err != nil {
		t.Fatal(err)
	}
	err := client.RoundTripContext(context.Background())
	if !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("round trip to closed peer read side = %v, want EPIPE", err)
	}
	client.objectsMu.RLock()
	remaining := len(client.objects)
	client.objectsMu.RUnlock()
	if remaining != 0 {
		t.Fatalf("failed sync retained %d callback objects", remaining)
	}
}

type dispatchWatcherContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *dispatchWatcherContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestDispatchReportsReadAndDeadlineResetFailuresAfterLocalClose(t *testing.T) {
	client, _ := newShutdownTestConnection(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &dispatchWatcherContext{Context: ctx, observed: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- client.dispatchWithCancellation(observed) }()
	select {
	case <-observed.observed:
	case <-time.After(time.Second):
		t.Fatal("dispatch cancellation watcher did not start")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) || !strings.Contains(err.Error(), "wl:") || !strings.Contains(err.Error(), "reset read deadline") {
			t.Fatalf("dispatch lost read or deadline-reset failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed transport did not unblock dispatch")
	}
}

func TestRegistryRejectsInterfaceNameMissingAlignmentPadding(t *testing.T) {
	var events []GlobalEvent
	registry := &Registry{}
	registry.SetGlobalHandler(func(event GlobalEvent) { events = append(events, event) })

	data := make([]byte, 10)
	PutUint32(data[0:4], 7)
	PutUint32(data[4:8], 2)
	data[8] = 'x'
	data[9] = 0
	registry.Dispatch(0, -1, data)
	if len(events) != 0 {
		t.Fatalf("truncated registry interface padding advertised a capability: %+v", events)
	}
}

func TestDispatchRejectsFrameWhosePayloadEndsEarly(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, err := ConnectContext(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}

	frame := make([]byte, 10)
	PutUint32(frame[0:4], 0)
	PutUint32(frame[4:8], 12<<16)
	frame[8], frame[9] = 1, 2
	if _, err := peer.Write(frame); err != nil {
		_ = peer.Close()
		t.Fatal(err)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := ctx.DispatchContext(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("dispatch of truncated Wayland frame = %v", err)
	}
}

func TestWaylandRegistryAndSHMExchangeRealWireAndFileDescriptors(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connected := make(chan *net.UnixConn, 1)
	go func() {
		peer, lintShadow24 := listener.AcceptUnix()
		if lintShadow24 == nil {
			connected <- peer
		}
	}()
	ctx, err := Connect(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()
	var peer *net.UnixConn
	select {
	case peer = <-connected:
		defer peer.Close()
	case <-time.After(time.Second):
		t.Fatal("synthetic compositor not reachable")
	}
	readMessage := func() ([]byte, []byte) {
		t.Helper()
		if lintShadow43 := peer.SetReadDeadline(time.Now().Add(time.Second)); lintShadow43 != nil {
			t.Fatal(lintShadow43)
		}
		raw := make([]byte, 4096)
		oob := make([]byte, 256)
		n, oobn, _, _, lintShadow48 := peer.ReadMsgUnix(raw, oob)
		if lintShadow48 != nil {
			t.Fatal(lintShadow48)
		}
		if n < 8 || int(Uint32(raw[4:8])>>16) != n {
			t.Fatalf("bad Wayland wire length: %x", raw[:n])
		}
		return raw[:n], oob[:oobn]
	}
	reg := &Registry{}
	ctx.Register(reg)
	target := &RawProxy{}
	ctx.Register(target)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if lintShadow63 := reg.BindContext(canceled, 7, "wl_output", 3, target.ID()); !errors.Is(lintShadow63, context.Canceled) {
		t.Fatalf("canceled registry bind wrote protocol: %v", lintShadow63)
	}
	if lintShadow66 := reg.Bind(7, "wl_output", 3, target.ID()); lintShadow66 != nil {
		t.Fatal(lintShadow66)
	}
	msg, oob := readMessage()
	if Uint32(msg[:4]) != reg.ID() || Uint32(msg[4:8])&0xffff != 0 || Uint32(msg[8:12]) != 7 || Uint32(msg[12:16]) != 10 || string(msg[16:25]) != "wl_output" || msg[25] != 0 || Uint32(msg[28:32]) != 3 || Uint32(msg[32:36]) != target.ID() || len(oob) != 0 {
		t.Fatalf("registry bind does not preserve object ID, name, interface, version, or new ID: %x oob=%x", msg, oob)
	}
	shm := &Shm{}
	ctx.Register(shm)
	input, err := os.CreateTemp(t.TempDir(), "shared-memory-*")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	content := []byte("pixels for synthetic compositor")
	if _, lintShadow81 := input.Write(content); lintShadow81 != nil {
		t.Fatal(lintShadow81)
	}
	before := len(ctx.objects)
	if pool, lintShadow85 := shm.CreatePoolContext(canceled, int(input.Fd()), 4096); !errors.Is(lintShadow85, context.Canceled) || pool != nil || len(ctx.objects) != before {
		t.Fatalf("canceled shared memory bind mutated registry: %v, %p", lintShadow85, pool)
	}
	pool, err := shm.CreatePool(int(input.Fd()), 4096)
	if err != nil {
		t.Fatal(err)
	}
	msg, oob = readMessage()
	if len(msg) != 16 || Uint32(msg[:4]) != shm.ID() || Uint32(msg[8:12]) != pool.ID() || Uint32(msg[12:16]) != 4096 {
		t.Fatalf("invalid shm create_pool wire: %x", msg)
	}
	cms, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		t.Fatal(err)
	}
	if len(cms) != 1 {
		t.Fatalf("expected exactly one shared memory fd, got %d", len(cms))
	}
	fds, err := syscall.ParseUnixRights(&cms[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(fds) != 1 {
		t.Fatalf("shared memory missing fd: %v", fds)
	}
	func() {
		fd := os.NewFile(uintptr(fds[0]), "synthetic compositor mapped memory")
		defer fd.Close()
		got := make([]byte, len(content))
		if _, lintShadow114 := fd.ReadAt(got, 0); lintShadow114 != nil || !bytes.Equal(got, content) {
			t.Fatalf("passed FD does not own source pixel bytes: %q %v", got, lintShadow114)
		}
	}()
	before = len(ctx.objects)
	if buf, lintShadow119 := pool.CreateBufferContext(canceled, 8, 64, 32, 256, 875713112); !errors.Is(lintShadow119, context.Canceled) || buf != nil || len(ctx.objects) != before {
		t.Fatalf("canceled SHM buffer bind mutated registry: %v, %p", lintShadow119, buf)
	}
	buffer, err := pool.CreateBuffer(8, 64, 32, 256, 875713112)
	if err != nil {
		t.Fatal(err)
	}
	msg, oob = readMessage()
	if len(msg) != 32 || Uint32(msg[:4]) != pool.ID() || Uint32(msg[8:12]) != buffer.ID() || Uint32(msg[12:16]) != 8 || Uint32(msg[16:20]) != 64 || Uint32(msg[20:24]) != 32 || Uint32(msg[24:28]) != 256 || Uint32(msg[28:32]) != 875713112 || len(oob) != 0 {
		t.Fatalf("invalid SHM buffer parameters: %x", msg)
	}
	if err := buffer.DestroyContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled buffer destroy: %v", err)
	}
	if _, ok := ctx.objects[buffer.ID()]; !ok {
		t.Fatal("canceled destruction incorrectly dropped live buffer")
	}
	if err := buffer.Destroy(); err != nil {
		t.Fatal(err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != buffer.ID() {
		t.Fatalf("buffer destroy wrong object: %x", msg)
	}
	if _, ok := ctx.objects[buffer.ID()]; ok {
		t.Fatal("destroyed buffer not unregistered")
	}
	if err := pool.DestroyContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pool destruction: %v", err)
	}
	if _, ok := ctx.objects[pool.ID()]; !ok {
		t.Fatal("canceled pool destruction dropped active pool")
	}
	if err := pool.Destroy(); err != nil {
		t.Fatal(err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != pool.ID() || Uint32(msg[4:8])&0xffff != 1 {
		t.Fatalf("pool destroy wrong wire request: %x", msg)
	}
	if _, ok := ctx.objects[pool.ID()]; ok {
		t.Fatal("destroyed pool not unregistered")
	}
	// Direct dispatch wrapper drives an actual callback over the socket.
	cb := &Callback{}
	ctx.Register(cb)
	fired := false
	cb.SetDoneHandler(func() { fired = true })
	callbackWire := make([]byte, 12)
	PutUint32(callbackWire[:4], cb.ID())
	PutUint32(callbackWire[4:8], 12<<16)
	PutUint32(callbackWire[8:12], 888)
	if _, err := peer.Write(callbackWire); err != nil {
		t.Fatal(err)
	}
	if err := DispatchContext(context.Background(), ctx); err != nil || !fired {
		t.Fatalf("wayland callback not dispatched from real wire: fired=%v err=%v", fired, err)
	}
	// The underlying socket must not have inherited a cancelled I/O deadline.
	if err := WriteMsg(ctx, []byte{1, 0, 0, 0, 0, 0, 8, 0}, nil); err != nil {
		t.Fatalf("prior cancellation leaked write deadline: %v", err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != 1 {
		t.Fatalf("wire wrapper lost message: %x", msg)
	}
}

// A Wayland request that sent a partial message or failed to reset an I/O
// deadline cannot be acknowledged as delivered. Preserve cancellation and
// transport errors together instead of losing the causal chain.
func TestWaylandWriteResultReportsCompleteTransportErrorMatrix(t *testing.T) {
	canceled := context.Canceled
	wireErr := errors.New("synthetic Wayland peer stopped reading")
	resetErr := errors.New("synthetic write deadline reset failed")
	for _, tc := range []struct {
		name                 string
		cancel, write, reset error
		n, oob               int
		want                 []error
		message              string
		ok                   bool
	}{
		{name: "complete", n: 8, oob: 4, ok: true},
		{name: "complete concurrent cancellation is acknowledged", cancel: canceled, n: 8, oob: 4, ok: true},
		{name: "data short", n: 4, oob: 4, message: "short write"},
		{name: "fd short", n: 8, oob: 2, message: "short write"},
		{name: "short both", n: 3, oob: 1, message: "short write"},
		{name: "short after cancellation", cancel: canceled, n: 6, oob: 4, want: []error{canceled}, message: "short write"},
		{name: "transport failure", write: wireErr, want: []error{wireErr}},
		{name: "failure with cancellation", cancel: canceled, write: wireErr, want: []error{canceled, wireErr}},
		{name: "failure plus reset", write: wireErr, reset: resetErr, want: []error{wireErr, resetErr}},
		{name: "failure plus cancellation and reset", cancel: canceled, write: wireErr, reset: resetErr, want: []error{canceled, wireErr, resetErr}},
		{name: "only reset failed", n: 8, oob: 4, reset: resetErr, want: []error{resetErr}},
		{name: "reset failed after cancellation", n: 8, oob: 4, cancel: canceled, reset: resetErr, want: []error{canceled, resetErr}},
		{name: "both short plus reset", n: 4, oob: 0, reset: resetErr, want: []error{resetErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := finishWriteResult(tc.cancel, tc.n, tc.oob, tc.write, tc.reset, 8, 4)
			if tc.ok {
				if err != nil {
					t.Fatalf("completed transport erroneously failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("transport failure acknowledged")
			}
			for _, cause := range tc.want {
				if !errors.Is(err, cause) {
					t.Fatalf("lost transport cause %v in %v", cause, err)
				}
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("missing failure detail %q: %v", tc.message, err)
			}
		})
	}
}

func TestPutUint32(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0x01020304)
	want := []byte{0x04, 0x03, 0x02, 0x01} // little-endian
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(0x01020304) = %v, want %v", buf, want)
	}
}

func TestUint32(t *testing.T) {
	buf := []byte{0x04, 0x03, 0x02, 0x01}
	got := Uint32(buf)
	if got != 0x01020304 {
		t.Errorf("Uint32 = 0x%x, want 0x01020304", got)
	}
}

func TestPutUint32_Zero(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0)
	want := []byte{0, 0, 0, 0}
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(0) = %v, want %v", buf, want)
	}
}

func TestPutUint32_Max(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0xffffffff)
	want := []byte{0xff, 0xff, 0xff, 0xff}
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(max) = %v, want %v", buf, want)
	}
}

func TestPutStr(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		// "hello" → length=6, "hello\0", padded to 8 bytes
		{
			"hello",
			[]byte{
				6, 0, 0, 0, // length = strlen+1 = 6
				'h', 'e', 'l', 'l', 'o', 0, // string + null
				0, 0, // padding to 8 bytes
			},
		},
		// empty string → length=1, null, padded to 4 bytes
		{
			"",
			[]byte{
				1, 0, 0, 0, // length = 1
				0,       // null terminator
				0, 0, 0, // padding to 4 bytes
			},
		},
		// "ab" → length=3, "ab" + 2 zeros (null + 1 pad) = 4 bytes
		{
			"ab",
			[]byte{
				3, 0, 0, 0, // length = 3
				'a', 'b', 0, 0, // string + null + 1 pad byte
			},
		},
		// "abc" → length=4, "abc\0", padded to 4 bytes (exact)
		{
			"abc",
			[]byte{
				4, 0, 0, 0, // length = 4
				'a', 'b', 'c', 0, // string + null (exactly 4 bytes)
			},
		},
		// "abcd" → length=5, "abcd\0", padded to 8 bytes
		{
			"abcd",
			[]byte{
				5, 0, 0, 0, // length = 5
				'a', 'b', 'c', 'd', 0, // string + null
				0, 0, 0, // padding to 8 bytes
			},
		},
	}
	for _, tc := range tests {
		got := putStr(nil, tc.input)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("putStr(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestPutStr_PrependToExistingBuffer(t *testing.T) {
	prefix := []byte{0xAA, 0xBB}
	buf := putStr(prefix, "x")
	// prefix + length(2) + "x" + 3 zeros (null + 2 pad to reach 4)
	want := []byte{0xAA, 0xBB, 2, 0, 0, 0, 'x', 0, 0, 0}
	if !bytes.Equal(buf, want) {
		t.Errorf("putStr with prefix = %v, want %v", buf, want)
	}
}

func TestRawProxy_Dispatch(t *testing.T) {
	var gotOpcode uint32
	var gotData []byte
	p := &RawProxy{
		OnEvent: func(opcode uint32, fd int, data []byte) {
			gotOpcode = opcode
			gotData = append([]byte{}, data...)
		},
	}
	p.Dispatch(3, -1, []byte{0x01, 0x02})
	if gotOpcode != 3 {
		t.Errorf("opcode = %d, want 3", gotOpcode)
	}
	if !bytes.Equal(gotData, []byte{0x01, 0x02}) {
		t.Errorf("data = %v, want [1 2]", gotData)
	}
}

func TestRawProxy_DispatchNilHandler(t *testing.T) {
	// Should not panic with nil OnEvent
	p := &RawProxy{}
	p.Dispatch(0, -1, nil) //nolint:errcheck
}

func TestBaseProxy_ID(t *testing.T) {
	var bp BaseProxy
	if bp.ID() != 0 {
		t.Errorf("zero-value ID = %d, want 0", bp.ID())
	}
	bp.SetID(42)
	if bp.ID() != 42 {
		t.Errorf("ID = %d, want 42", bp.ID())
	}
}

func TestBaseProxy_Ctx(t *testing.T) {
	var bp BaseProxy
	if bp.Ctx() != nil {
		t.Errorf("zero-value Ctx = %v, want nil", bp.Ctx())
	}
}

func TestShmObjectsUnregisterAfterDestroy(t *testing.T) {
	ctx := &Context{}
	shm := &Shm{}
	shm.SetCtx(ctx)

	pool, err := shm.CreatePool(0, 4)
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	buf, err := pool.CreateBuffer(0, 1, 1, 4, 0)
	if err != nil {
		t.Fatalf("CreateBuffer: %v", err)
	}
	if got := len(ctx.objects); got != 2 {
		t.Fatalf("registered SHM objects = %d, want 2", got)
	}

	if err := buf.Destroy(); err != nil {
		t.Fatalf("Buffer.Destroy: %v", err)
	}
	if got := len(ctx.objects); got != 1 {
		t.Fatalf("registered objects after buffer destroy = %d, want 1", got)
	}
	if err := pool.Destroy(); err != nil {
		t.Fatalf("ShmPool.Destroy: %v", err)
	}
	if got := len(ctx.objects); got != 0 {
		t.Fatalf("registered objects after pool destroy = %d, want 0", got)
	}
}

func TestRegistry_Dispatch_GlobalEvent(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })

	// Build a wl_registry.global event using putStr for the interface string.
	// Layout: name(4) + interface(string: length+bytes+pad) + version(4)
	data := make([]byte, 0)
	data = append(data, 1, 0, 0, 0) // name = 1
	data = putStr(data, "wl_seat")  // string: len=7, "wl_seat\0" + pad to 8
	data = append(data, 3, 0, 0, 0) // version = 3

	r.Dispatch(0, -1, data)
	if got.Name != 1 {
		t.Errorf("name = %d, want 1", got.Name)
	}
	if got.Interface != "wl_seat" {
		t.Errorf("interface = %q, want %q", got.Interface, "wl_seat")
	}
	if got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}
}

func TestRegistry_Dispatch_GlobalRemoveEvent(t *testing.T) {
	var got uint32
	r := &Registry{}
	r.SetGlobalRemoveHandler(func(name uint32) { got = name })

	r.Dispatch(1, -1, []byte{7, 0, 0, 0})
	if got != 7 {
		t.Fatalf("removed global name = %d, want 7", got)
	}
}

func TestRegistry_Dispatch_MalformedData(t *testing.T) {
	called := false
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { called = true })

	// Less than 8 bytes
	r.Dispatch(0, -1, []byte{1, 2, 3})
	if called {
		t.Fatal("handler called on truncated data")
	}

	// slen claims 100 bytes but only 8 present
	r.Dispatch(0, -1, []byte{1, 0, 0, 0, 100, 0, 0, 0})
	if called {
		t.Fatal("handler called on overlong slen")
	}

	// slen is 0
	r.Dispatch(0, -1, []byte{1, 0, 0, 0, 0, 0, 0, 0})
	if called {
		t.Fatal("handler called on zero slen")
	}

	// A string length that cannot fit in the event must not reach indexing.
	overflowLength := []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}
	r.Dispatch(0, -1, overflowLength)
	if called {
		t.Fatal("handler called on overflowing slen")
	}

	// Wayland strings include a terminating NUL; reject malformed strings.
	withoutNUL := []byte{1, 0, 0, 0, 4, 0, 0, 0, 'x', 'y', 'z', 'w', 1, 0, 0, 0}
	r.Dispatch(0, -1, withoutNUL)
	if called {
		t.Fatal("handler called on string without NUL terminator")
	}
}

func TestRegistry_Dispatch_NoHandler(t *testing.T) {
	r := &Registry{}
	// Should not panic
	r.Dispatch(0, -1, []byte{1, 0, 0, 0, 4, 0, 0, 0, 'x', 0, 0, 0, 1, 0, 0, 0}) //nolint:errcheck
}

func TestRegistry_Dispatch_WrongOpcode(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })
	r.Dispatch(1, -1, []byte{1, 0, 0, 0}) // opcode 1, not global
	if got.Name != 0 {
		t.Error("handler should not be called for non-zero opcode")
	}
}

func TestRegistry_Dispatch_ShortData(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })
	r.Dispatch(0, -1, []byte{1, 0}) // too short
	if got.Name != 0 {
		t.Error("handler should not be called for short data")
	}
}

func TestCallback_Dispatch_Done(t *testing.T) {
	called := false
	c := &Callback{}
	c.SetDoneHandler(func() { called = true })
	c.Dispatch(0, -1, nil)
	if !called {
		t.Error("done handler not called")
	}
}

func TestCallback_Dispatch_WrongOpcode(t *testing.T) {
	called := false
	c := &Callback{}
	c.SetDoneHandler(func() { called = true })
	c.Dispatch(1, -1, nil)
	if called {
		t.Error("handler should not be called for non-zero opcode")
	}
}

func TestCallback_DispatchNilHandler(t *testing.T) {
	c := &Callback{}
	c.Dispatch(0, -1, nil) // should not panic
}

func TestContext_WriteMsg_NilConn(t *testing.T) {
	// Context with nil conn (test construction) should be a no-op
	ctx := &Context{}
	if err := ctx.WriteMsg([]byte{1}, nil); err != nil {
		t.Errorf("WriteMsg on nil conn returned error: %v", err)
	}
}

func TestContext_Dispatch_NilConn(t *testing.T) {
	ctx := &Context{}
	if err := ctx.Dispatch(); err != nil {
		t.Errorf("Dispatch on nil conn returned error: %v", err)
	}
}

func TestContextDispatchRejectsUnalignedMessageSize(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
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
		var header [8]byte
		PutUint32(header[4:8], 10<<16) // header + 2-byte, non-aligned body
		_, writeErr := conn.Write(header[:])
		serverDone <- writeErr
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()

	if err := ctx.Dispatch(); err == nil || !strings.Contains(err.Error(), "4-byte aligned") {
		t.Fatalf("Dispatch error = %v, want alignment error", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server write: %v", err)
	}
}

func TestContextDispatchContextCancelsBlockedRead(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		var buf [1]byte
		_, _ = conn.Read(buf[:])
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()
	<-accepted

	cancelCtx, cancel := context.WithCancel(context.Background())
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- ctx.DispatchContext(cancelCtx) }()
	cancel()

	select {
	case err := <-dispatchDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("DispatchContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DispatchContext did not unblock after cancellation")
	}
	if err := ctx.WriteMsg([]byte{0}, nil); err != nil {
		t.Fatalf("connection unusable after canceled dispatch: %v", err)
	}
	_ = ctx.Close()
	listener.Close()
	<-serverDone
}

func TestContextWriteMsgContextCancelsBlockedWrite(t *testing.T) { //nolint:gocyclo // deterministically drives the socket into a blocked-write state.
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	releaseServer := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-releaseServer
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()
	<-accepted

	cancelCtx, cancel := context.WithCancel(context.Background())
	writeDone := make(chan error, 1)
	progress := make(chan struct{}, 1)
	go func() {
		chunk := make([]byte, 4096)
		for {
			if err := ctx.WriteMsgContext(cancelCtx, chunk, nil); err != nil {
				writeDone <- err
				return
			}
			select {
			case progress <- struct{}{}:
			default:
			}
		}
	}()
	// Let successful writes accumulate to fill the peer's send buffer, then
	// wait for progress to stop before canceling the blocked write.
	select {
	case <-progress:
	case err := <-writeDone:
		t.Fatalf("WriteMsgContext returned before the blocked write: %v", err)
	case <-time.After(time.Second):
		t.Fatal("WriteMsgContext made no progress while filling the socket")
	}
	for {
		select {
		case <-progress:
			// Keep draining progress until the next write blocks.
		case <-time.After(10 * time.Millisecond):
			goto blocked
		case err := <-writeDone:
			t.Fatalf("WriteMsgContext returned before cancellation: %v", err)
		}
	}

blocked:
	cancel()
	select {
	case err := <-writeDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WriteMsgContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteMsgContext did not unblock after cancellation")
	}
	close(releaseServer)
	<-serverDone
}

func TestContextCloseInterruptsBlockedWrite(t *testing.T) { //nolint:gocyclo // deterministically drives the socket into a blocked-write state.
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	releaseServer := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-releaseServer
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	<-accepted

	releaseOnce := sync.OnceFunc(func() { close(releaseServer) })
	writerExited := make(chan struct{})
	cleanup := func() {
		// Closing the raw socket is the emergency cleanup path if an assertion
		// below catches a regression that leaves Close waiting on the writer.
		_ = ctx.conn.Close()
		select {
		case <-writerExited:
		case <-time.After(time.Second):
		}
		releaseOnce()
		<-serverDone
	}
	t.Cleanup(cleanup)

	writeDone := make(chan error, 1)
	progress := make(chan struct{}, 1)
	go func() {
		defer close(writerExited)
		chunk := make([]byte, 4096)
		for {
			if err := ctx.WriteMsg(chunk, nil); err != nil {
				writeDone <- err
				return
			}
			select {
			case progress <- struct{}{}:
			default:
			}
		}
	}()

	select {
	case <-progress:
	case err := <-writeDone:
		t.Fatalf("WriteMsg returned before the blocked write: %v", err)
	case <-time.After(time.Second):
		t.Fatal("WriteMsg made no progress while filling the socket")
	}

	stallTimer := time.NewTimer(50 * time.Millisecond)
	defer stallTimer.Stop()
	for {
		select {
		case <-progress:
			if !stallTimer.Stop() {
				select {
				case <-stallTimer.C:
				default:
				}
			}
			stallTimer.Reset(50 * time.Millisecond)
		case err := <-writeDone:
			t.Fatalf("WriteMsg returned before Close: %v", err)
		case <-stallTimer.C:
			goto blocked
		}
	}

blocked:
	closeDone := make(chan error, 1)
	go func() { closeDone <- ctx.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind the active socket write")
	}

	select {
	case err := <-writeDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("WriteMsg error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked WriteMsg did not return after Close")
	}

	if err := ctx.WriteMsg([]byte{1}, nil); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteMsg after Close = %v, want net.ErrClosed", err)
	}
	if err := ctx.DispatchContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("DispatchContext after Close = %v, want net.ErrClosed", err)
	}
	if err := ctx.RoundTripContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("RoundTripContext after Close = %v, want net.ErrClosed", err)
	}
	called := false
	if err := ctx.WithOperationContext(context.Background(), func() error {
		called = true
		return nil
	}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WithOperationContext after Close = %v, want net.ErrClosed", err)
	}
	if called {
		t.Fatal("WithOperationContext callback ran after Close")
	}
}

func TestContextWithOperationContextCancelsQueuedAdmission(t *testing.T) {
	ctx := &Context{}
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- ctx.WithOperationContext(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	baseCtx, cancel := context.WithCancel(context.Background())
	cancelCtx := &operationAdmissionObservedContext{Context: baseCtx, selectReady: make(chan struct{})}
	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- ctx.WithOperationContext(cancelCtx, func() error { return nil })
	}()
	<-cancelCtx.selectReady
	cancel()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not unblock after cancellation")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first operation: %v", err)
	}
}

func TestContextWithOperationContextRejectsQueuedCallbackAfterClose(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()
	ctx, err := ConnectContext(context.Background(), sock)
	if err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}
	peer, err := listener.AcceptUnix()
	if err != nil {
		_ = ctx.Close()
		t.Fatalf("AcceptUnix: %v", err)
	}
	t.Cleanup(func() {
		_ = ctx.Close()
		_ = peer.Close()
	})

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseOwner := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseOwner)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- ctx.WithOperationContext(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	queuedCtx := &operationAdmissionObservedContext{Context: context.Background(), selectReady: make(chan struct{})}
	queuedDone := make(chan error, 1)
	called := false
	go func() {
		queuedDone <- ctx.WithOperationContext(queuedCtx, func() error { called = true; return nil })
	}()
	<-queuedCtx.selectReady
	if err := ctx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	releaseOwner()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("queued operation after close = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not resume after the current operation released admission")
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("active operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active operation did not release admission")
	}
	if called {
		t.Fatal("queued operation callback ran after transport closure")
	}
}

type operationAdmissionObservedContext struct {
	context.Context
	selectReady chan struct{}
	once        sync.Once
}

func (c *operationAdmissionObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.selectReady) })
	return c.Context.Done()
}

func TestContextWriteMsgContextCancelsQueuedWrite(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()
	<-accepted

	gate := ctx.writeGateChannel()
	<-gate
	cancelCtx, cancel := context.WithCancel(context.Background())
	observedCtx := &operationAdmissionObservedContext{Context: cancelCtx, selectReady: make(chan struct{})}
	writeDone := make(chan error, 1)
	go func() { writeDone <- ctx.WriteMsgContext(observedCtx, []byte{1}, nil) }()
	select {
	case <-observedCtx.selectReady:
	case <-time.After(time.Second):
		t.Fatal("queued write did not reach admission wait")
	}
	cancel()
	select {
	case err := <-writeDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued write error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued write did not unblock after cancellation")
	}
	gate <- struct{}{}
	<-serverDone
}

func TestContextDispatchContextCancelsQueuedAdmission(t *testing.T) {
	testContextQueuedAdmissionCancellation(t,
		func(ctx *Context) chan struct{} { return ctx.dispatchGateChannel() },
		func(ctx *Context, cancel context.Context) error { return ctx.DispatchContext(cancel) },
	)
}

func TestContextRoundTripContextCancelsQueuedAdmission(t *testing.T) {
	testContextQueuedAdmissionCancellation(t,
		func(ctx *Context) chan struct{} { return ctx.roundTripGateChannel() },
		func(ctx *Context, cancel context.Context) error { return ctx.RoundTripContext(cancel) },
	)
}

func testContextQueuedAdmissionCancellation(
	t *testing.T,
	gateFunc func(*Context) chan struct{},
	operation func(*Context, context.Context) error,
) {
	t.Helper()
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()
	<-accepted

	gate := gateFunc(ctx)
	<-gate
	cancelCtx, cancel := context.WithCancel(context.Background())
	observedCtx := &operationAdmissionObservedContext{Context: cancelCtx, selectReady: make(chan struct{})}
	operationDone := make(chan error, 1)
	go func() { operationDone <- operation(ctx, observedCtx) }()
	select {
	case <-observedCtx.selectReady:
	case <-time.After(time.Second):
		t.Fatal("queued operation did not reach admission wait")
	}
	cancel()
	select {
	case err := <-operationDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not unblock after cancellation")
	}
	gate <- struct{}{}
	<-serverDone
}

func TestContext_Close_NilConn(t *testing.T) {
	// Context with nil conn (test construction) should be a no-op (not panic).
	ctx := &Context{}
	if err := ctx.Close(); err != nil {
		t.Errorf("Close on nil conn returned error: %v", err)
	}
}

func TestContextRegisterConcurrent(t *testing.T) {
	ctx := &Context{}
	const count = 100
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx.Register(&RawProxy{})
		}()
	}
	wg.Wait()

	ctx.objectsMu.RLock()
	got := len(ctx.objects)
	ctx.objectsMu.RUnlock()
	if got != count {
		t.Fatalf("registered proxy count = %d, want %d", got, count)
	}
}

func TestContextWithOperationAllowsReentrantStateChanges(t *testing.T) {
	ctx := &Context{}
	if err := ctx.WithOperation(func() error {
		ctx.Register(&RawProxy{})
		ctx.SetProxy(100, &RawProxy{})
		return nil
	}); err != nil {
		t.Fatalf("WithOperation: %v", err)
	}
	ctx.objectsMu.RLock()
	defer ctx.objectsMu.RUnlock()
	if len(ctx.objects) != 2 {
		t.Fatalf("object count = %d, want 2", len(ctx.objects))
	}
}

func TestContextWithOperationSerializesCalls(t *testing.T) {
	ctx := &Context{}
	var active, maxActive int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ctx.WithOperation(func() error {
				current := atomic.AddInt32(&active, 1)
				for {
					old := atomic.LoadInt32(&maxActive)
					if current <= old || atomic.CompareAndSwapInt32(&maxActive, old, current) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&active, -1)
				return nil
			}); err != nil {
				t.Errorf("WithOperation: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("maximum concurrent operations = %d, want 1", got)
	}
}

func TestContextRoundTripUnregistersCallbacks(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var request [12]byte
		for {
			if _, readErr := io.ReadFull(conn, request[:]); readErr != nil {
				return
			}
			var event [8]byte
			PutUint32(event[0:], Uint32(request[8:12]))
			PutUint32(event[4:], 8<<16)
			if _, writeErr := conn.Write(event[:]); writeErr != nil {
				return
			}
		}
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	for i := 0; i < 20; i++ {
		if err := ctx.RoundTrip(); err != nil {
			t.Fatalf("RoundTrip %d: %v", i, err)
		}
	}
	if err := ctx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-serverDone

	ctx.objectsMu.RLock()
	defer ctx.objectsMu.RUnlock()
	if got := len(ctx.objects); got != 0 {
		t.Fatalf("object count after round trips = %d, want 0", got)
	}
}

func TestDisplayCreationUnregistersProxyAfterCanceledWrite(t *testing.T) {
	ctx := &Context{}
	display := NewDisplay(ctx)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if registry, err := display.GetRegistryContext(canceled); !errors.Is(err, context.Canceled) || registry != nil {
		t.Fatalf("GetRegistryContext() = (%v, %v), want (nil, context.Canceled)", registry, err)
	}
	if callback, err := display.SyncContext(canceled); !errors.Is(err, context.Canceled) || callback != nil {
		t.Fatalf("SyncContext() = (%v, %v), want (nil, context.Canceled)", callback, err)
	}
	ctx.objectsMu.RLock()
	defer ctx.objectsMu.RUnlock()
	if got := len(ctx.objects); got != 0 {
		t.Fatalf("registered objects after canceled creation = %d, want 0", got)
	}
}

func TestRegistry_BindMessageLayout(t *testing.T) {
	// Verify that Registry.Bind produces the correct wire format.
	// We can't call Bind without a real ctx, but we can test the encoding
	// indirectly by checking putStr output.
	var buf []byte
	buf = put32(buf, 5)  // registry id
	buf = put32(buf, 0)  // placeholder for size|opcode
	buf = put32(buf, 10) // name
	buf = putStr(buf, "wl_seat")
	buf = put32(buf, 3)                      // version
	buf = put32(buf, 20)                     // new_id
	PutUint32(buf[4:], uint32(len(buf))<<16) // opcode 0 = bind

	// Verify the buffer has the right structure
	if Uint32(buf[0:4]) != 5 {
		t.Errorf("registry id = %d, want 5", Uint32(buf[0:4]))
	}
	sizeOpcode := Uint32(buf[4:8])
	size := int(sizeOpcode >> 16)
	if size != len(buf) {
		t.Errorf("message size = %d, want %d", size, len(buf))
	}
	if sizeOpcode&0xffff != 0 {
		t.Errorf("opcode = %d, want 0", sizeOpcode&0xffff)
	}
}
