package wl

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A closed Wayland transport cannot accept further writes, dispatches,
// round-trips or logical operations even though its proxy registry remains.
func TestWaylandClosedTransportRejectsEveryPublicOperation(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, err := ConnectContext(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	callbackCalled := false
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"write", func() error { return ctx.WriteMsgContext(context.Background(), []byte{0, 0, 0, 0}, nil) }},
		{"dispatch", func() error { return ctx.DispatchContext(context.Background()) }},
		{"round trip", func() error { return ctx.RoundTripContext(context.Background()) }},
		{"operation", func() error {
			return ctx.WithOperationContext(context.Background(), func() error { callbackCalled = true; return nil })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("closed Wayland transport accepted operation: %v", err)
			}
		})
	}
	if callbackCalled {
		t.Fatal("closed transport ran operation callback")
	}
}

func TestWaylandCanceledDialRejectsBeforeSocketAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if session, err := ConnectContext(ctx, "/not/a/real/socket"); session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial accessed filesystem: session=%v err=%v", session, err)
	}
}

func TestResolveWaylandSocketPathRejectsUnresolvedRelativeNames(t *testing.T) {
	for _, tc := range []struct{ display, runtime, want string }{
		{"", "/tmp/safe", ""},
		{"wl-0", "", ""},
		{"/tmp/explicit-wayland", "/run/ignored", "/tmp/explicit-wayland"},
		{"wayland-4", "/tmp/private-runtime", "/tmp/private-runtime/wayland-4"},
	} {
		if got := ResolveSocketPath(tc.display, tc.runtime); got != tc.want {
			t.Errorf("display %q runtime %q resolved to %q, wanted %q", tc.display, tc.runtime, got, tc.want)
		}
	}
}

// connectSyntheticDefaultDisplay owns a private Unix socket and compositor;
// the caller explicitly closes the client and checks peer shutdown.
func connectSyntheticDefaultDisplay(t *testing.T) (*Context, <-chan error) {
	t.Helper()
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	peerDone := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerDone <- acceptErr
			return
		}
		peerDone <- privateSessionWaylandPeer(peer)
	}()
	ctx, err := Connect(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	return ctx, peerDone
}

func assertPrivateCompositorStopped(t *testing.T, peerDone <-chan error, message string) {
	t.Helper()
	select {
	case err := <-peerDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("synthetic compositor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func TestDefaultDisplayProtocolMethodsUsePrivateSyntheticWaylandServer(t *testing.T) {
	ctx, peerDone := connectSyntheticDefaultDisplay(t)
	display := NewDisplay(ctx)
	if display.Context() != ctx {
		t.Fatal("display lost original transport")
	}
	reg, err := display.GetRegistry()
	if err != nil || reg == nil || reg.ID() <= 1 {
		t.Fatalf("GetRegistry returned %v %v", reg, err)
	}
	globals := make(map[string]bool)
	reg.SetGlobalHandler(func(ev GlobalEvent) { globals[ev.Interface] = true })
	cb, err := display.Sync()
	if err != nil || cb == nil || cb.ID() <= reg.ID() {
		t.Fatalf("Sync returned %v %v", cb, err)
	}
	if err := ctx.RoundTrip(); err != nil {
		t.Fatalf("context convenience round trip: %v", err)
	}
	if err := display.RoundTrip(); err != nil {
		t.Fatalf("display convenience round trip: %v", err)
	}
	if !globals["wl_output"] || !globals["wl_seat"] {
		t.Fatalf("default registry wrapper did not dispatch authoritative globals: %+v", globals)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	assertPrivateCompositorStopped(t, peerDone, "compositor did not terminate after transport closure")
}

func TestCallbackDispatchAndWaylandObjectDispatchBoundaries(t *testing.T) {
	fired := 0
	cb := &Callback{}
	cb.SetDoneHandler(func() { fired++ })
	cb.Dispatch(3, -1, nil)
	if fired != 0 {
		t.Fatal("unknown callback opcode fired Done")
	}
	cb.Dispatch(0, -1, nil)
	if fired != 1 {
		t.Fatalf("Done dispatched %d times", fired)
	}
	(&Shm{}).Dispatch(0, -1, nil)
	(&ShmPool{}).Dispatch(0, -1, nil)
	(&Buffer{}).Dispatch(0, -1, nil)
}

func TestListGlobalsAndSocketReachabilityUseOnlySyntheticCompositor(t *testing.T) {
	if got := ListGlobals(""); got != nil {
		t.Fatalf("empty socket unexpectedly enumerated globals: %+v", got)
	}
	notSocket := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(notSocket, []byte("not a compositor"), 0600); err != nil {
		t.Fatal(err)
	}
	if SocketReachable(notSocket) || ListGlobals(notSocket) != nil {
		t.Fatal("regular file accepted as Wayland socket")
	}
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			peerDone <- err
			return
		}
		peerDone <- privateSessionWaylandPeer(conn)
	}()
	if !SocketReachable(socket) {
		t.Fatal("private compositor socket not recognized")
	}
	names := ListGlobals(socket)
	if !names["wl_output"] || !names["wl_seat"] || len(names) != 2 {
		t.Fatalf("unexpected compositor globals %+v", names)
	}
	select {
	case err := <-peerDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("compositor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListGlobals leaked session transport")
	}
}

// Low-level Wayland operations accept a nil caller context as an unrestricted
// request. With no connected transport they must remain inert rather than
// dereference nil or acquire a socket that is not owned by the caller.
func TestNilCallerContextAndMissingWaylandTransportAreSafe(t *testing.T) {
	var ctx Context
	if err := ctx.WriteMsgContext(nil, []byte{1, 2, 3, 4}, nil); err != nil {
		t.Fatalf("unconnected Wayland write with nil context: %v", err)
	}
	if err := ctx.DispatchContext(nil); err != nil {
		t.Fatalf("unconnected Wayland dispatch with nil context: %v", err)
	}
	if err := ctx.RoundTripContext(nil); err != nil {
		t.Fatalf("unconnected Wayland round-trip with nil context: %v", err)
	}
	called := 0
	if err := ctx.WithOperationContext(nil, func() error { called++; return nil }); err != nil || called != 1 {
		t.Fatalf("nil caller context could not run safe operation: calls=%d err=%v", called, err)
	}
}

// Cancellation must take precedence over missing transport in each protocol
// operation, without executing a user callback.
func TestCanceledWaylandProtocolOperationsRejectBeforeTransport(t *testing.T) {
	var ctx Context
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"write", func() error { return ctx.WriteMsgContext(canceled, nil, nil) }},
		{"dispatch", func() error { return ctx.DispatchContext(canceled) }},
		{"round trip", func() error { return ctx.RoundTripContext(canceled) }},
		{"operation", func() error { return ctx.WithOperationContext(canceled, func() error { called = true; return nil }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request accepted: %v", err)
			}
		})
	}
	if called {
		t.Fatal("canceled operation invoked callback")
	}
}

// cancelAfterAdmissionContext lets a test cancel exactly at the post-gate check.
type cancelAfterAdmissionContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
	calls   int
}

func (c *cancelAfterAdmissionContext) Err() error {
	c.calls++
	if c.calls == 2 {
		c.once.Do(func() { close(c.checked) })
		<-c.Context.Done()
	}
	return c.Context.Err()
}

func TestContextOperationsObserveCancellationAfterAdmissionBeforeTransport(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*Context, context.Context) error
	}{
		{name: "write", run: func(ctx *Context, cancel context.Context) error {
			return ctx.WriteMsgContext(cancel, []byte{1}, nil)
		}},
		{name: "dispatch", run: func(ctx *Context, cancel context.Context) error {
			return ctx.DispatchContext(cancel)
		}},
		{name: "round trip", run: func(ctx *Context, cancel context.Context) error {
			return ctx.RoundTripContext(cancel)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			ctx, peerRead := newWaylandAdmissionTestContext(t)
			baseCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observedCtx := &cancelAfterAdmissionContext{Context: baseCtx, checked: make(chan struct{})}
			operationDone := make(chan error, 1)
			go func() { operationDone <- operation.run(ctx, observedCtx) }()

			select {
			case <-observedCtx.checked:
			case <-time.After(time.Second):
				t.Fatal("operation did not reach its post-admission cancellation check")
			}
			cancel()
			select {
			case err := <-operationDone:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("operation error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("operation did not return after cancellation")
			}

			if err := ctx.Close(); err != nil {
				t.Fatalf("close Wayland connection: %v", err)
			}
			select {
			case data := <-peerRead:
				if len(data) != 0 {
					t.Fatalf("canceled operation wrote %d transport bytes", len(data))
				}
			case <-time.After(time.Second):
				t.Fatal("peer read did not finish after connection close")
			}
		})
	}
}

func newWaylandAdmissionTestContext(t *testing.T) (*Context, <-chan []byte) {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "wlgate-")
	if err != nil {
		t.Fatalf("create short Wayland socket directory: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "wayland.sock"), Net: "unix"})
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen for Wayland connection: %v", err)
	}
	accepted := make(chan struct{})
	peerRead := make(chan []byte, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerRead <- nil
			return
		}
		defer peer.Close()
		close(accepted)
		buf := make([]byte, 32)
		n, _ := peer.Read(buf)
		peerRead <- append([]byte(nil), buf[:n]...)
	}()
	ctx, err := Connect(filepath.Join(dir, "wayland.sock"))
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("connect to Wayland test peer: %v", err)
	}
	t.Cleanup(func() {
		_ = ctx.Close()
		_ = listener.Close()
		_ = os.RemoveAll(dir)
	})
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("Wayland peer did not accept the connection")
	}
	return ctx, peerRead
}

// A dispatched payload omits the object id and the size/opcode header, so the
// four wl_output.mode arguments start at offset zero in the order flags, width,
// height, refresh. Reading width and height one field late yields the height
// and the refresh rate instead.
func TestDecodeOutputModeReadsArgumentsInProtocolOrder(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], OutputModeCurrent) // flags
	PutUint32(data[4:8], 3840)              // width
	PutUint32(data[8:12], 2160)             // height
	PutUint32(data[12:16], 60000)           // refresh

	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags != OutputModeCurrent {
		t.Fatalf("flags = %d, want %d", mode.Flags, OutputModeCurrent)
	}
	if mode.Width != 3840 || mode.Height != 2160 {
		t.Fatalf("size = %dx%d, want 3840x2160", mode.Width, mode.Height)
	}
	if mode.Refresh != 60000 {
		t.Fatalf("refresh = %d, want 60000", mode.Refresh)
	}
}

// Distinguishable field values catch a shift by one field, which repeating the
// same value would hide.
func TestDecodeOutputModeRejectsOffByOneFieldReads(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], 7)
	PutUint32(data[4:8], 11) // width
	PutUint32(data[8:12], 22)
	PutUint32(data[12:16], 33)

	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags != 7 || mode.Width != 11 || mode.Height != 22 || mode.Refresh != 33 {
		t.Fatalf("decoded %+v, want flags=7 width=11 height=22 refresh=33", mode)
	}
	// The historical off-by-one read produced height and refresh as the size.
	if mode.Width == mode.Height || mode.Height == mode.Refresh {
		t.Fatalf("fields are not distinguishable, so a shift would go unnoticed: %+v", mode)
	}
}

// A truncated payload is rejected rather than decoded from adjacent memory.
func TestDecodeOutputModeRejectsShortPayloads(t *testing.T) {
	for _, size := range []int{0, 4, 8, 12, 15} {
		if _, ok := DecodeOutputMode(make([]byte, size)); ok {
			t.Fatalf("DecodeOutputMode accepted a %d-byte payload", size)
		}
	}
	if _, ok := DecodeOutputMode(make([]byte, 16)); !ok {
		t.Fatal("DecodeOutputMode rejected a 16-byte payload")
	}
}

// The current-mode flag is what consumers use to adopt a mode.
func TestOutputModeCurrentFlagIsTheLowBit(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], OutputModeCurrent)
	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags&OutputModeCurrent == 0 {
		t.Fatalf("flags %d do not report the current mode", mode.Flags)
	}

	PutUint32(data[0:4], 0)
	mode, ok = DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags&OutputModeCurrent != 0 {
		t.Fatalf("flags %d report the current mode", mode.Flags)
	}
}

func TestWithOperationRunsCallback(t *testing.T) {
	ran := false
	if err := WithOperation(&Context{}, func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("WithOperation: %v", err)
	}
	if !ran {
		t.Fatal("WithOperation did not run its callback")
	}
}

// Invalid wl_registry.bind metadata must be rejected locally, before
// the protocol request could cause a compositor error or disconnect.
func TestRegistryBindRejectsInvalidMetadataBeforeTransmission(t *testing.T) {
	for _, tc := range []struct {
		name, iface    string
		version, newID uint32
	}{
		{"empty interface", "", 1, 21},
		{"embedded NUL", "wl_output\x00fake", 1, 21},
		{"invalid UTF-8", "wl_output\xff", 1, 21},
		{"zero version", "wl_output", 0, 21},
		{"zero object ID", "wl_output", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{}
			reg := &Registry{}
			reg.SetCtx(ctx)
			reg.SetID(2)
			err := reg.BindContext(context.Background(), 42, tc.iface, tc.version, tc.newID)
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed registry bind returned %v", err)
			}
		})
	}
}

// Registry advertisements are used directly to select protocol interfaces.
// Malformed names must not be normalized into a different bindable identity.
func TestRegistryRejectsMalformedGlobalInterfaceNames(t *testing.T) {
	for _, tc := range []struct{ name, iface string }{
		{"empty interface", ""},
		{"embedded NUL", "wl_output\x00spoofed"},
		{"invalid UTF8", "wl_output\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := 0
			r := &Registry{}
			r.SetGlobalHandler(func(GlobalEvent) { got++ })
			r.Dispatch(0, -1, registryGlobalData(51, tc.iface, 1))
			if got != 0 {
				t.Fatalf("published malformed global %q", tc.iface)
			}
		})
	}
}

// wl_registry.global has three mandatory arguments (name, interface, version).
// A compositor must not be able to advertise a usable capability using an
// incomplete event that omits the version field.
func TestRegistryRejectsTruncatedGlobalVersionBeforePublishing(t *testing.T) {
	var events []GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { events = append(events, ev) })
	complete := registryGlobalData(17, "wl_output", 4)
	if len(complete) < 4 {
		t.Fatal("invalid test fixture")
	}
	r.Dispatch(0, -1, complete[:len(complete)-4])
	if len(events) != 0 {
		t.Fatalf("truncated global advertised: %+v", events)
	}
	r.Dispatch(0, -1, complete)
	if len(events) != 1 || events[0] != (GlobalEvent{Name: 17, Interface: "wl_output", Version: 4}) {
		t.Fatalf("valid complete global was lost: %+v", events)
	}
}

// Registry interface versions begin at one. A complete advertisement with
// version zero must not be published as a bindable interface.
func TestRegistryRejectsZeroVersionGlobalAndRetainsValidAdvertisement(t *testing.T) {
	var got []GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(event GlobalEvent) { got = append(got, event) })
	r.Dispatch(0, -1, registryGlobalData(41, "wl_output", 0))
	if len(got) != 0 {
		t.Fatalf("invalid version-zero global was advertised: %+v", got)
	}
	r.Dispatch(0, -1, registryGlobalData(42, "wl_output", 1))
	if len(got) != 1 || got[0] != (GlobalEvent{Name: 42, Interface: "wl_output", Version: 1}) {
		t.Fatalf("valid minimum-version global not published exactly once: %+v", got)
	}
}

// A truncated global_remove event must not mutate the compositor's advertised
// interface inventory. A later complete event must still revoke the global.
func TestRegistryGlobalRemoveIgnoresTruncatedEventsAndAcceptsValidRemoval(t *testing.T) {
	s := &Session{}
	r := &Registry{}
	r.SetGlobalHandler(s.addGlobal)
	r.SetGlobalRemoveHandler(s.removeGlobal)
	r.Dispatch(0, -1, registryGlobalData(17, "wl_output", 3))
	if got := s.GlobalsSnapshot(); len(got) != 1 || got[0].Name != 17 {
		t.Fatalf("expected initial advertised output, got %+v", got)
	}
	for _, malformed := range [][]byte{nil, {}, {17}, {17, 0}, {17, 0, 0}} {
		r.Dispatch(1, -1, malformed)
		if got := s.GlobalsSnapshot(); len(got) != 1 || got[0].Name != 17 {
			t.Fatalf("truncated event %v revoked output: %+v", malformed, got)
		}
	}
	r.Dispatch(2, -1, []byte{17, 0, 0, 0})
	if got := s.GlobalsSnapshot(); len(got) != 1 {
		t.Fatalf("unrecognized removal opcode revoked output: %+v", got)
	}
	r.Dispatch(1, -1, []byte{17, 0, 0, 0})
	if got := s.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("complete removal event did not revoke output: %+v", got)
	}
}

func TestContextRoundTripHandlesImmediateCallback(t *testing.T) {
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
			binary.LittleEndian.PutUint32(event[0:4], binary.LittleEndian.Uint32(request[8:12]))
			binary.LittleEndian.PutUint32(event[4:8], 8<<16)
			if _, writeErr := conn.Write(event[:]); writeErr != nil {
				return
			}
		}
	}()

	ctx, err := Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ctx.Close()

	for i := 0; i < 100; i++ {
		roundTripCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := ctx.RoundTripContext(roundTripCtx)
		cancel()
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i, err)
		}
	}

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Wayland round-trip server did not stop")
	}
}

func TestNewSessionContextCancelsRegistryRoundTrip(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverReady := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var request [12]byte
		if _, readErr := io.ReadFull(conn, request[:]); readErr != nil {
			return
		}
		if _, readErr := io.ReadFull(conn, request[:]); readErr != nil {
			return
		}
		close(serverReady)
		var one [1]byte
		_, _ = conn.Read(one[:])
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		session, openErr := NewSessionContext(ctx, sock)
		if session != nil {
			_ = session.Close()
		}
		result <- openErr
	}()
	<-serverReady
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("NewSessionContext error = %v, want context.Canceled", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Wayland server did not stop after canceled setup")
	}

	sessionCacheMu.Lock()
	_, cached := sessionCache[sock]
	sessionCacheMu.Unlock()
	if cached {
		t.Fatal("canceled Wayland session was cached")
	}
}

func TestSessionSetupContextPreservesPolicyDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	setupCtx, setupCancel := sessionSetupContext(ctx)
	defer setupCancel()
	got, ok := setupCtx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("session setup deadline = %v, present=%v, want caller policy deadline %v", got, ok, deadline)
	}
}

func TestWaylandGlobalCacheCopiesUpsertsAndRemovesAcrossSharedHandles(t *testing.T) {
	canonical := &Session{Sock: "synthetic-shared-registry"}
	ref := &sessionRef{sess: canonical, refs: 2}
	a, b := newSessionHandle(ref), newSessionHandle(ref)
	a.addGlobal(GlobalEvent{Name: 42, Interface: "wl_output", Version: 2})
	a.addGlobal(GlobalEvent{Name: 7, Interface: "wl_seat", Version: 1})
	a.addGlobal(GlobalEvent{Name: 42, Interface: "wl_output", Version: 4})
	want := []GlobalEvent{{Name: 7, Interface: "wl_seat", Version: 1}, {Name: 42, Interface: "wl_output", Version: 4}}
	if got := b.GlobalsSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shared upsert replaced wrong global: got=%+v want=%+v", got, want)
	}
	observed := b.GlobalsSnapshot()
	observed[0].Interface = "untrusted modification"
	if got := a.GlobalsSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutation affected canonical registry: %+v", got)
	}
	b.removeGlobal(42)
	a.removeGlobal(99)
	if got := a.GlobalsSnapshot(); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("global removal from shared handle lost unrelated entry: %+v", got)
	}
	b.removeGlobal(7)
	if got := a.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("registry remained after complete removal: %+v", got)
	}
}

func TestWaylandSessionNilAndUncachedPartialOperationsAreSafe(t *testing.T) {
	var nilSession *Session
	nilSession.addGlobal(GlobalEvent{Name: 1})
	nilSession.removeGlobal(1)
	if got := nilSession.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("nil session exposed globals: %+v", got)
	}
	if err := nilSession.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if err := nilSession.SyncContext(context.Background()); err != nil {
		t.Fatalf("nil sync: %v", err)
	}
	partial := &Session{Sock: "synthetic-unacquired-runtime"}
	if err := partial.SyncContext(context.Background()); err != nil {
		t.Fatalf("partial sync: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("partial close: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("idempotent partial close: %v", err)
	}
	partial.Display = &Display{}
	if err := partial.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed partial session re-entered compositor: %v", err)
	}
}

func TestWaylandSessionFinalReferenceWithoutTransportReleasesCacheExactlyOnce(t *testing.T) {
	sock := "synthetic-final-null-context-no-transport"
	canonical := &Session{Sock: sock}
	ref := &sessionRef{sess: canonical, refs: 2}
	sessionCacheMu.Lock()
	if _, exists := sessionCache[sock]; exists {
		sessionCacheMu.Unlock()
		t.Fatal("synthetic key is already owned")
	}
	sessionCache[sock] = ref
	sessionCacheMu.Unlock()
	t.Cleanup(func() { sessionCacheMu.Lock(); delete(sessionCache, sock); sessionCacheMu.Unlock() })
	first := newSessionHandle(ref)
	second := newSessionHandle(ref)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("duplicate release: %v", err)
	}
	sessionCacheMu.Lock()
	active := sessionCache[sock]
	sessionCacheMu.Unlock()
	if active != ref || active.refs != 1 {
		t.Fatalf("first close released other holder: ref=%p count=%d", active, ref.refs)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, stillCached := sessionCache[sock]
	sessionCacheMu.Unlock()
	if stillCached || ref.refs != 0 {
		t.Fatalf("final release leaked cached session: cached=%v refs=%d", stillCached, ref.refs)
	}
}

func TestWaylandGlobalsSnapshotConcurrentWithGlobalLifecycle(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.addGlobal(GlobalEvent{Name: uint32(id + 1), Interface: "wl_output", Version: uint32(i + 1)})
				for _, global := range s.GlobalsSnapshot() {
					if global.Name == 0 || global.Version == 0 {
						t.Errorf("invalid global: %+v", global)
					}
				}
				if i%5 == 0 {
					s.removeGlobal(uint32(id + 1))
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(s.GlobalsSnapshot()) > 8 {
		t.Fatal("duplicate global names introduced under concurrent updates")
	}
}

type observedSessionCacheContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *observedSessionCacheContext) Err() error {
	c.once.Do(func() { close(c.checked) })
	return c.Context.Err()
}

func TestCanceledCallerDoesNotAcquireCachedWaylandSession(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	ref := &sessionRef{sess: &Session{Sock: sock}, refs: 1}
	sessionCacheMu.Lock()
	if sessionCache == nil {
		sessionCache = make(map[string]*sessionRef)
	}
	if sessionCache[sock] != nil {
		sessionCacheMu.Unlock()
		t.Fatalf("unexpected session already cached at %q", sock)
	}
	sessionCache[sock] = ref
	locked := true
	defer func() {
		if locked {
			sessionCacheMu.Unlock()
		}
	}()
	t.Cleanup(func() {
		sessionCacheMu.Lock()
		if sessionCache[sock] == ref {
			delete(sessionCache, sock)
		}
		sessionCacheMu.Unlock()
	})

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedSessionCacheContext{Context: parent, checked: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		session, err := NewSessionContext(ctx, sock)
		if session != nil {
			_ = session.Close()
		}
		result <- err
	}()

	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("session setup did not inspect the caller context before cache admission")
	}
	cancel()
	sessionCacheMu.Unlock()
	locked = false

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cache admission returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled cache admission did not return")
	}

	sessionCacheMu.Lock()
	got, refs := sessionCache[sock], ref.refs
	if got == ref {
		delete(sessionCache, sock)
	}
	sessionCacheMu.Unlock()
	if got != ref || refs != 1 {
		t.Fatalf("canceled caller changed cache ownership: cached=%v refs=%d, want original session and one reference", got == ref, refs)
	}
}

// Simultaneous constructors can all finish a private Wayland registry
// handshake before any of them enters the session cache. Only one transport
// may remain owned, while each caller receives a distinct close handle.
func TestWaylandSessionConcurrentFirstAcquisitionSharesOneCachedTransport(t *testing.T) {
	sock := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var peers sync.WaitGroup
	var peerMu sync.Mutex
	var peerErrors []error
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			peers.Add(1)
			go func() {
				defer peers.Done()
				if err := privateSessionWaylandPeer(conn); err != nil {
					peerMu.Lock()
					peerErrors = append(peerErrors, err)
					peerMu.Unlock()
				}
			}()
		}
	}()
	const n = 10
	start := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	type result struct {
		sess *Session
		err  error
	}
	results := make(chan result, n)
	for range n {
		go func() { <-start; sess, err := NewSessionContext(ctx, sock); results <- result{sess, err} }()
	}
	close(start)
	handles := make([]*Session, 0, n)
	for range n {
		res := <-results
		if res.err != nil || res.sess == nil {
			for _, h := range handles {
				_ = h.Close()
			}
			t.Fatalf("concurrent registry negotiation failed: session=%v err=%v", res.sess, res.err)
		}
		handles = append(handles, res.sess)
	}
	shared := handles[0].Ctx
	seenHandles := make(map[*Session]bool, len(handles))
	for i, h := range handles {
		if h.Ctx != shared {
			t.Fatalf("session handle %d did not share cached transport", i)
		}
		if seenHandles[h] {
			t.Fatalf("session handle %d reused another caller's close handle", i)
		}
		seenHandles[h] = true
	}
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	var count int
	if ref != nil {
		count = ref.refs
	}
	sessionCacheMu.Unlock()
	if count != n {
		t.Fatalf("shared session reference count=%d want %d", count, n)
	}
	for i := 0; i < n-1; i++ {
		if err := handles[i].Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := handles[n-1].SyncContext(ctx); err != nil {
		t.Fatalf("earlier closes terminated final handle: %v", err)
	}
	if err := handles[n-1].Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("final close retained stale cache entry")
	}
	if err := handles[n-1].SyncContext(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed handle sync=%v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acceptDone:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not exit")
	}
	finished := make(chan struct{})
	go func() { peers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("losing Wayland connections leaked")
	}
	peerMu.Lock()
	defer peerMu.Unlock()
	if len(peerErrors) != 0 {
		t.Fatalf("private Wayland peers: %v", peerErrors)
	}
}

// An untrusted compositor can disconnect at either stage of the session
// handshake. Neither failure may publish a cached session or live transport.
func TestSessionHandshakeRejectsDisconnectedCompositorWithoutCaching(t *testing.T) {
	for _, phase := range []string{"after-registry-request", "after-sync-request"} {
		t.Run(phase, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				peer, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer peer.Close()
				var header [8]byte
				if _, err = io.ReadFull(peer, header[:]); err != nil {
					done <- err
					return
				}
				if phase == "after-registry-request" {
					done <- nil
					return
				}
				var registryRequest [4]byte
				if _, err = io.ReadFull(peer, registryRequest[:]); err != nil {
					done <- err
					return
				}
				if _, err = io.ReadFull(peer, header[:]); err != nil {
					done <- err
					return
				}
				var syncRequest [4]byte
				if _, err = io.ReadFull(peer, syncRequest[:]); err != nil {
					done <- err
					return
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			session, openErr := NewSessionContext(ctx, socket)
			if session != nil {
				_ = session.Close()
				t.Fatal("disconnected compositor produced live session")
			}
			if openErr == nil {
				t.Fatal("disconnected compositor was accepted")
			}
			if phase == "after-registry-request" && !strings.Contains(openErr.Error(), "registry round-trip") {
				t.Fatalf("missing registry round-trip failure detail: %v", openErr)
			}
			if phase == "after-sync-request" && !strings.Contains(openErr.Error(), "registry round-trip") {
				t.Fatalf("missing sync failure detail: %v", openErr)
			}
			if err := <-done; err != nil {
				t.Fatalf("synthetic compositor failed: %v", err)
			}
			sessionCacheMu.Lock()
			_, cached := sessionCache[socket]
			sessionCacheMu.Unlock()
			if cached {
				t.Fatal("failed Wayland handshake published cached session")
			}
		})
	}
}

// Missing or zero-value display contexts cannot be used for a registry
// round-trip, and a zero-value transport cannot block on a network operation.
func TestUnconnectedWaylandProtocolDispatchAndDisplayRoundTripAreInert(t *testing.T) {
	var transport Context
	if err := transport.dispatch(); err != nil {
		t.Fatalf("unconnected dispatch: %v", err)
	}
	var display *Display
	if err := display.RoundTrip(); err != nil {
		t.Fatalf("nil display round trip: %v", err)
	}
	if err := (&Display{}).RoundTrip(); err != nil {
		t.Fatalf("unconnected display round trip: %v", err)
	}
	if _, err := ConnectContext(context.Background(), "/this/wayland/socket/does/not/exist"); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("missing compositor returned unexpected connection result: %v", err)
	}
}

// Cancellation during the final cache-admission lock must neither publish
// an unowned Wayland connection nor acquire an unrelated replacement owner.
func TestWaylandCancelWhileFinalSessionCacheAdmissionBlocked(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "unowned"
		if replacement {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			sock := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ready := make(chan struct{})
			reply := make(chan struct{})
			sent := make(chan struct{})
			peerDone := make(chan error, 1)
			go func() {
				peer, err := listener.AcceptUnix()
				if err != nil {
					peerDone <- err
					return
				}
				defer peer.Close()
				_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
				var header [8]byte
				for {
					if _, err := io.ReadFull(peer, header[:]); err != nil {
						peerDone <- err
						return
					}
					n := int(Uint32(header[4:]) >> 16)
					if n < 8 || n > 65535 {
						peerDone <- errors.New("invalid compositor request")
						return
					}
					body := make([]byte, n-8)
					if _, err := io.ReadFull(peer, body); err != nil {
						peerDone <- err
						return
					}
					if Uint32(header[:4]) == 1 && Uint32(header[4:])&0xffff == 0 {
						callback := Uint32(body)
						close(ready)
						<-reply
						if err := writeSessionEvent(peer, callback, 0, make([]byte, 4)); err != nil {
							peerDone <- err
							return
						}
						close(sent)
						var one [1]byte
						_, err := peer.Read(one[:])
						peerDone <- err
						return
					}
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				s, err := NewSessionContext(ctx, sock)
				if s != nil {
					_ = s.Close()
				}
				done <- err
			}()
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("synthetic compositor never reached registry sync")
			}
			sessionCacheMu.Lock()
			var owner *sessionRef
			if replacement {
				owner = &sessionRef{sess: &Session{Sock: sock}, refs: 1}
				sessionCache[sock] = owner
			}
			close(reply)
			select {
			case <-sent:
			case <-time.After(time.Second):
				sessionCacheMu.Unlock()
				t.Fatal("compositor never completed registry sync")
			}
			// The handshake reply is now available while cache admission is held.
			// Give the client time to reach the lock, then cancel before unlocking.
			time.Sleep(10 * time.Millisecond)
			cancel()
			sessionCacheMu.Unlock()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled cache acquisition: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled cache acquisition hung")
			}
			sessionCacheMu.Lock()
			got, found := sessionCache[sock]
			if got == owner && owner != nil {
				delete(sessionCache, sock)
			}
			sessionCacheMu.Unlock()
			if replacement {
				if got != owner || owner.refs != 1 {
					t.Fatalf("replacement cache corrupted: got=%p owner=%p refs=%d", got, owner, owner.refs)
				}
			} else if found {
				t.Fatal("canceled uncached session published a connection")
			}
			select {
			case err := <-peerDone:
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					t.Fatalf("canceled handshake leaked connection: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled Wayland connection not closed")
			}
		})
	}
}

func writeSessionEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	pkt := make([]byte, 8+len(body))
	PutUint32(pkt[0:], sender)
	PutUint32(pkt[4:], uint32(len(pkt))<<16|opcode)
	copy(pkt[8:], body)
	_, err := conn.Write(pkt)
	return err
}
func privateSessionWaylandPeer(conn *net.UnixConn) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registryID uint32
	syncCount := 0
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("private compositor read: %w", err)
		}
		sender, encoded := Uint32(header[:4]), Uint32(header[4:])
		size := int(encoded >> 16)
		if size < 8 || size > 65535 {
			return fmt.Errorf("malformed Wayland request length %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		switch {
		case sender == 1 && encoded&0xffff == 1:
			if len(body) != 4 {
				return fmt.Errorf("get_registry malformed %x", body)
			}
			registryID = Uint32(body)
		case sender == 1 && encoded&0xffff == 0:
			cb := Uint32(body)
			syncCount++
			switch syncCount {
			case 1:
				for _, ev := range []struct {
					name    uint32
					iface   string
					version uint32
				}{
					{32, "wl_output", 4}, {15, "wl_seat", 7}, {50, "wl_output", 2},
				} {
					if err := writeSessionEvent(conn, registryID, 0, registryGlobalData(ev.name, ev.iface, ev.version)); err != nil {
						return err
					}
				}
			case 2:
				payload := make([]byte, 4)
				PutUint32(payload, 32)
				if err := writeSessionEvent(conn, registryID, 1, payload); err != nil {
					return err
				}
			}
			payload := make([]byte, 4)
			if err := writeSessionEvent(conn, cb, 0, payload); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unrecognized Wayland wire sender=%d opcode=%d", sender, encoded&0xffff)
		}
	}
}
func TestRealSyntheticWaylandSessionCacheRefcountsGlobalChangesAndContextCancellation(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, lintShadow91 := listener.AcceptUnix()
		if lintShadow91 != nil {
			serverDone <- lintShadow91
			return
		}
		serverDone <- privateSessionWaylandPeer(conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := NewSessionContext(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	expected := []GlobalEvent{{Name: 15, Interface: "wl_seat", Version: 7}, {Name: 32, Interface: "wl_output", Version: 4}, {Name: 50, Interface: "wl_output", Version: 2}}
	initial := first.GlobalsSnapshot()
	if len(initial) != len(expected) {
		t.Fatalf("registry populated %+v instead of %+v", initial, expected)
	}
	for i, e := range expected {
		if initial[i] != e {
			t.Fatalf("global[%d]=%+v want %+v", i, initial[i], e)
		}
	}
	second, err := NewSessionContext(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.Ctx != second.Ctx {
		t.Fatal("cached Wayland sessions must share transport but not close handles")
	}
	initial[0].Interface = "untrusted mutation"
	if first.GlobalsSnapshot()[0].Interface == "untrusted mutation" {
		t.Fatal("global snapshot leaked mutable backing slice")
	}
	if err := second.Sync(); err != nil {
		t.Fatal(err)
	}
	got := first.GlobalsSnapshot()
	if len(got) != 2 || got[0].Name != 15 || got[1].Name != 50 {
		t.Fatalf("Wayland registry removal not propagated to shared handles: %+v", got)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if acquired, err := NewSessionContext(canceled, socket); !errors.Is(err, context.Canceled) || acquired != nil {
		t.Fatalf("canceled cache hit acquired live session: %v %v", acquired, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.SyncContext(ctx); err != nil {
		t.Fatalf("first close terminated second live handle: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.SyncContext(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("final close allowed sync: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("private compositor shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("final reference did not close Wayland transport")
	}
}
func TestWaylandGlobalsSortStableUnderConcurrentIndependentSnapshots(t *testing.T) {
	s := &Session{globals: []GlobalEvent{{Name: 5, Interface: "wl_output", Version: 3}, {Name: 5, Interface: "wl_output", Version: 2}, {Name: 1, Interface: "wl_seat", Version: 1}}}
	sortGlobals(s.globals)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				got := s.GlobalsSnapshot()
				if len(got) != 3 || got[0].Name != 1 || got[1].Version != 2 || got[2].Version != 3 {
					t.Errorf("wrong stable snapshot %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// An untrusted compositor may advertise different interfaces or versions
// under the same numeric global name. Sort the observed snapshot with all
// tie-breakers so diagnostics and consumers do not depend on event arrival
// order, while retaining conflicting declarations for inspection.
func TestWaylandRegistryOrderingIsDeterministicForConflictingGlobalNames(t *testing.T) {
	observations := []GlobalEvent{
		{Name: 12, Interface: "wl_seat", Version: 3},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 4, Interface: "wl_output", Version: 1},
		{Name: 12, Interface: "wl_output", Version: 2},
		{Name: 12, Interface: "wp_viewporter", Version: 1},
	}
	want := []GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 1},
		{Name: 12, Interface: "wl_output", Version: 2},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_seat", Version: 3},
		{Name: 12, Interface: "wp_viewporter", Version: 1},
	}
	sortGlobals(observations)
	if !reflect.DeepEqual(observations, want) {
		t.Fatalf("conflicting compositor declarations sorted incorrectly: got=%+v want=%+v", observations, want)
	}
}

// A delayed Close from an old connection must never decrement or evict the
// replacement connection under the same compositor socket cache key.
func TestWaylandStaleSessionHandleCannotReleaseReplacementCacheOwner(t *testing.T) {
	sock := "synthetic-reused-compositor-cache-key"
	oldCanonical := &Session{Sock: sock, Display: &Display{}}
	oldRef := &sessionRef{sess: oldCanonical, refs: 1}
	currentCanonical := &Session{Sock: sock}
	currentRef := &sessionRef{sess: currentCanonical, refs: 2}
	sessionCacheMu.Lock()
	if sessionCache[sock] != nil {
		sessionCacheMu.Unlock()
		t.Fatal("test key in use")
	}
	sessionCache[sock] = currentRef
	sessionCacheMu.Unlock()
	t.Cleanup(func() {
		sessionCacheMu.Lock()
		if sessionCache[sock] == currentRef {
			delete(sessionCache, sock)
		}
		sessionCacheMu.Unlock()
	})
	stale := newSessionHandle(oldRef)
	if err := stale.Close(); err != nil {
		t.Fatalf("stale close: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("stale double close: %v", err)
	}
	if err := stale.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("stale handle became usable: %v", err)
	}
	sessionCacheMu.Lock()
	got := sessionCache[sock]
	count := currentRef.refs
	sessionCacheMu.Unlock()
	if got != currentRef || count != 2 {
		t.Fatalf("stale close damaged replacement: owner=%p want=%p refs=%d", got, currentRef, count)
	}
	a, b := newSessionHandle(currentRef), newSessionHandle(currentRef)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	got = sessionCache[sock]
	count = currentRef.refs
	sessionCacheMu.Unlock()
	if got != currentRef || count != 1 {
		t.Fatalf("replacement unexpectedly retired on first close: owner=%p refs=%d", got, count)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("final owner close left cache entry")
	}
}

func TestSessionGlobalsSnapshotPreservesRepeatedInterfacesAndRemoval(t *testing.T) {
	session := &Session{globals: make([]GlobalEvent, 0)}
	registry := &Registry{}
	registry.SetGlobalHandler(session.addGlobal)
	registry.SetGlobalRemoveHandler(session.removeGlobal)

	registry.Dispatch(0, -1, registryGlobalData(12, "wl_output", 3))
	registry.Dispatch(0, -1, registryGlobalData(4, "wl_output", 4))
	registry.Dispatch(0, -1, registryGlobalData(9, "wl_seat", 7))

	got := session.GlobalsSnapshot()
	want := []GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 9, Interface: "wl_seat", Version: 7},
		{Name: 12, Interface: "wl_output", Version: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("globals length = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("global[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	registry.Dispatch(1, -1, []byte{4, 0, 0, 0})
	got = session.GlobalsSnapshot()
	if len(got) != 2 || got[0].Name != 9 || got[1].Name != 12 {
		t.Fatalf("globals after removal = %+v, want names 9, 12", got)
	}

	got[0].Interface = "mutated"
	if snapshot := session.GlobalsSnapshot(); snapshot[0].Interface == "mutated" {
		t.Fatal("GlobalsSnapshot returned session-owned storage")
	}
}

func registryGlobalData(name uint32, iface string, version uint32) []byte {
	data := make([]byte, 0, 8+len(iface)+8)
	var nameData [4]byte
	PutUint32(nameData[:], name)
	data = append(data, nameData[:]...)
	data = putStr(data, iface)
	var versionData [4]byte
	PutUint32(versionData[:], version)
	data = append(data, versionData[:]...)
	return data
}

func TestNewSession_CacheHit(t *testing.T) {
	// Clear the cache first.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-cache"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	// NewSession should return the cached session and increment refcount.
	s, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s == fakeSess {
		t.Fatal("NewSession returned the cache owner instead of a close handle")
	}
	if s.Sock != sock {
		t.Errorf("sock = %q, want %q", s.Sock, sock)
	}

	// Refcount should now be 2.
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("session not in cache")
		return
	}
	if ref.refs != 2 {
		t.Errorf("refcount = %d, want 2", ref.refs)
	}

	// Close should decrement but not destroy.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("session should still be in cache")
		return
	}
	if ref.refs != 1 {
		t.Errorf("refcount after close = %d, want 1", ref.refs)
	}
}

func TestSessionHandleRejectsOperationsAfterClose(t *testing.T) {
	canonical := &Session{Ctx: &Context{}, Display: &Display{}}
	handle := &Session{
		Ctx:     canonical.Ctx,
		Display: canonical.Display,
		ref:     &sessionRef{sess: canonical, refs: 2},
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := handle.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed handle SyncContext error = %v, want %v", err, net.ErrClosed)
	}
	if err := canonical.SyncContext(context.Background()); err != nil {
		t.Fatalf("remaining session reference became unusable: %v", err)
	}
}

func TestNewSession_CloseDecrementsRefcount(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-refcount"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	// Get two references.
	s1, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession 1: %v", err)
	}
	s2, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession 2: %v", err)
	}
	if s1 == s2 {
		t.Fatal("session acquisitions should have distinct close handles")
	}

	// Refcount should be 3 (initial 1 + 2 calls).
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref.refs != 3 {
		t.Errorf("refcount = %d, want 3", ref.refs)
	}

	// Close s1 → refcount 2.
	if err := s1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil || ref.refs != 2 {
		t.Errorf("refcount after close 1 = %d, want 2", ref.refs)
	}

	// Close s2 → refcount 1.
	if err := s2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil || ref.refs != 1 {
		t.Errorf("refcount after close 2 = %d, want 1", ref.refs)
	}

	// Session should still be in cache (refcount > 0).
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if !exists {
		t.Error("session should still be in cache while refcount > 0")
	}
}

func TestSessionCloseRemovesCacheAtZeroRefs(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-zero"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	if err := fakeSess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("session remained in cache after final close")
	}
}

func TestNewSessionCacheConcurrentHitAndClose(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-concurrent"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := NewSession(sock)
			if err != nil {
				t.Errorf("NewSession: %v", err)
				return
			}
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()

	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("original cached session was removed")
		return
	}
	if ref.refs != 1 {
		t.Fatalf("refcount = %d, want 1", ref.refs)
	}
}

func TestSessionCloseDoesNotReleaseReplacementSession(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()
	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-replacement"
	first := &Session{Sock: sock, Ctx: &Context{}}
	firstRef := &sessionRef{sess: first, refs: 1}
	second := &Session{Sock: sock, Ctx: &Context{}}
	secondRef := &sessionRef{sess: second, refs: 1}
	sessionCacheMu.Lock()
	sessionCache[sock] = firstRef
	sessionCacheMu.Unlock()

	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	sessionCacheMu.Lock()
	sessionCache[sock] = secondRef
	sessionCacheMu.Unlock()

	if err := first.Close(); err != nil {
		t.Fatalf("repeated first Close: %v", err)
	}
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref != secondRef || ref.refs != 1 {
		t.Fatalf("replacement session was modified: ref=%p refs=%d", ref, ref.refs)
	}
}

func TestNewSession_NotInCache(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	// Try to get a session for a non-existent socket.
	sock := "/nonexistent/test.sock"
	_, err := NewSession(sock)
	if err == nil {
		t.Fatal("expected error for non-existent socket")
	}
	t.Logf("got expected error: %v", err)

	// Cache should be empty (failed connection not cached).
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Error("failed session should not be cached")
	}
}

func TestNewSession_CacheMissCreatesNew(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	// NewSession for an unreachable socket should fail but also
	// should not pollute the cache.
	sock := "/nonexistent/new.sock"
	_, err := NewSession(sock)
	if err == nil {
		t.Fatal("expected error")
	}
	// The session should have been created then closed (ctx.Close() path).
	// Since the connect failed, the session won't be in the cache.
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Error("failed session should not remain in cache")
	}
}

// A session constructed around a live but uncached Wayland connection must
// own its socket. Closing that session must immediately terminate the private
// compositor peer, even though no cache entry or reference exists to release.
func TestUncachedWaylandSessionClosesItsLiveSocket(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptUnix()
		if err != nil {
			peerDone <- err
			return
		}
		defer peer.Close()
		_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		var one [1]byte
		_, err = peer.Read(one[:])
		peerDone <- err
	}()
	ctx, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{Sock: socket, Ctx: ctx}
	if err := session.Close(); err != nil {
		t.Fatalf("uncached session close: %v", err)
	}
	if !ctx.closed.Load() {
		t.Fatal("uncached Wayland connection remains open")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("idempotent uncached session close: %v", err)
	}
	select {
	case err := <-peerDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("private compositor not notified of socket close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("private compositor remained blocked after uncached session closed")
	}
}

// TestListGlobalsBoundsWedgedSocket verifies that env-detection helpers give
// up on a compositor socket that accepts connections but never replies,
// instead of blocking forever. This pins the round-trip budget that keeps
// Open responsive to wedged compositors.
func TestListGlobalsBoundsWedgedSocket(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	sock := filepath.Join(dir, "wedged-wayland-0")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept connections but never speak the Wayland protocol: exactly what
	// a frozen or SIGSTOPed compositor looks like from the client side.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				conn.Close()
			}()
		}
	}()

	if !SocketReachable(sock) {
		t.Fatal("test setup: socket should be reachable")
	}

	old := defaultRoundTripTimeout
	defaultRoundTripTimeout = 250 * time.Millisecond
	defer func() { defaultRoundTripTimeout = old }()

	finished := make(chan bool, 1)
	go func() {
		s, err := NewSession(sock)
		if err == nil {
			s.Close()
		}
		finished <- true
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		close(done) // unblock helper goroutines before failing hard
		t.Fatal("NewSession against a wedged socket did not return within the round-trip budget")
	}

	// The public detection helper must also stay bounded.
	globalsDone := make(chan bool, 1)
	go func() {
		ListGlobals(sock)
		globalsDone <- true
	}()
	select {
	case <-globalsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ListGlobals against a wedged socket did not return within the round-trip budget")
	}

	_ = os.Remove(sock)
}
