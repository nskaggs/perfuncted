package input

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"golang.org/x/sys/unix"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"
)

func newAuthorizedEISOperationFixture(t *testing.T) (*RemoteDesktopBackend, *net.UnixConn) {
	t.Helper()
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(server) }()
	sender, err := setupEISender(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-peerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("peer didn't finish protocol setup: %v", ctx.Err())
	}
	backend := newRemoteDesktopBackend(sender, &portalSession{})
	t.Cleanup(func() { _ = backend.Close() })
	return backend, server
}

func assertSyntheticEISOperation(t *testing.T, server *net.UnixConn, wantObject uint64, wantOpcode uint32) {
	t.Helper()
	event, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if event.object != wantObject || event.opcode != wantOpcode {
		t.Fatalf("unexpected EIS request = %+v want object %d opcode %d", event, wantObject, wantOpcode)
	}
	frame, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if frame.object != 0xff00000000000012 || frame.opcode != 3 {
		t.Fatalf("missing device frame after operation: %+v", frame)
	}
}

func TestEISAuthorizedInputExercisesTextMouseAndScroll(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.TypeLiteral(ctx, "é hello"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+105, 2)
	if err := backend.MouseMove(ctx, 300, 400); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+101, 1)
	if err := backend.MouseClick(ctx, 300, 400, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+101, 1)
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	if err := backend.ScrollUp(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+104, 2)
	if err := backend.ScrollDown(ctx, 2); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+104, 2)
	if err := backend.ScrollLeft(ctx, 3); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+104, 2)
	if err := backend.ScrollRight(ctx, 4); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+104, 2)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	if err := backend.MouseUp(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+103, 1)
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+103, 1)
	_, _, err := backend.PointerLocation(ctx)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("pointer query should declare unsupported: %v", err)
	}
	if len(backend.Diagnostics()) < 2 {
		t.Fatal("missing authorized backend diagnostics")
	}
	coord, err := backend.PointerCoordinateSpace(ctx)
	if err != nil || coord.ScaleX != 1 || coord.ScaleY != 1 {
		t.Fatalf("coordinate space %+v, %v", coord, err)
	}
}

func TestEISAuthorizedCloseReleasesHeldInput(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+103, 1)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if key.object != eiServerIDFloor+103 || key.opcode != 1 || binary.NativeEndian.Uint32(key.body[4:8]) != 0 {
		t.Fatalf("held key not released during close: %+v", key)
	}
	frame, err := readSyntheticEISFrame(server)
	if err != nil || frame.object != 0xff00000000000012 {
		t.Fatalf("key release lacked frame: %+v %v", frame, err)
	}
	button, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if button.object != eiServerIDFloor+102 || button.opcode != 1 || binary.NativeEndian.Uint32(button.body[4:8]) != 0 {
		t.Fatalf("held button not released during close: %+v", button)
	}
	frame, err = readSyntheticEISFrame(server)
	if err != nil || frame.object != 0xff00000000000012 {
		t.Fatalf("button release lacked frame: %+v %v", frame, err)
	}
	stop, err := readSyntheticEISFrame(server)
	if err != nil || stop.object != 0xff00000000000012 || stop.opcode != 2 {
		t.Fatalf("device not stopped: %+v, %v", stop, err)
	}
}

func TestEISAuthorizedTypeValidatesFullSequenceBeforeSending(t *testing.T) { //nolint:gocyclo // Assert EIS request/response order and held modifier lifetime.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.Type(ctx, "hello"); err != nil {
		t.Fatalf("literal text: %v", err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+105, 2)

	if err := backend.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("chord: %v", err)
	}
	want := []struct {
		key     uint32
		pressed uint32
	}{{29, 1}, {30, 1}, {30, 0}, {29, 0}}
	for i, expected := range want {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		if request.object != eiServerIDFloor+103 || request.opcode != 1 || len(request.body) != 8 {
			t.Fatalf("chord transition %d incorrect EIS event: %+v", i, request)
		}
		gotKey := binary.NativeEndian.Uint32(request.body[:4])
		gotState := binary.NativeEndian.Uint32(request.body[4:8])
		if gotKey != expected.key || gotState != expected.pressed {
			t.Fatalf("chord transition %d = key %d state %d want %+v", i, gotKey, gotState, expected)
		}
		frame, err := readSyntheticEISFrame(server)
		if err != nil || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("missing frame for chord transition %d: %+v %v", i, frame, err)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("temporary chord keys were retained: %v", backend.heldKeys)
	}
	for _, invalid := range []string{"{ctrl+not-a-key}", string([]byte{0xff})} {
		if err := backend.Type(ctx, invalid); err == nil {
			t.Fatalf("accepted invalid key sequence %q", invalid)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("invalid key sequence held keys: %v", backend.heldKeys)
	}
}

func TestEISAuthorizedTypePreservesExplicitlyHeldModifier(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.KeyDown(ctx, "shift"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+103, 1)
	if err := backend.Type(ctx, "{shift+a}"); err != nil {
		t.Fatal(err)
	}
	for _, wantState := range []uint32{1, 0} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		if request.object != eiServerIDFloor+103 || binary.NativeEndian.Uint32(request.body[:4]) != 30 || binary.NativeEndian.Uint32(request.body[4:8]) != wantState {
			t.Fatalf("held shift produced unexpected key transition: %+v", request)
		}
		frame, err := readSyntheticEISFrame(server)
		if err != nil || frame.object != 0xff00000000000012 {
			t.Fatalf("missing keyboard frame: %+v %v", frame, err)
		}
	}
	if !backend.heldKeys[42] {
		t.Fatalf("explicitly held shift was accidentally released: %v", backend.heldKeys)
	}
	if err := backend.KeyUp(ctx, "shift"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+103, 1)
}

// TestEICompleteAuthorizedSession exercises protocol negotiation, device grants,
// event framing, mutation and revocation using a real Unix socket and a local
// synthetic EIS peer. No compositor or user desktop is involved.
func TestEICompleteAuthorizedSession(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	server, ctx, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connID = uint64(0xff00000000000010)
	const seatID = uint64(0xff00000000000011)
	const devID = uint64(0xff00000000000012)
	// A single resumed device carries the mandatory pointer, button and keyboard
	// interfaces. Its desktop region has a negative-free origin and explicit bounds.
	stream := eiRequest(0, 2, append(append(eiU32(9), eiU64(connID)...), eiU32(1)...))
	stream = append(stream, eiRequest(connID, 1, eiU64(seatID))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(seatID, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seatID || bind.opcode != 1 || len(bind.body) != 8 || binary.NativeEndian.Uint64(bind.body) != 31 {
		t.Fatalf("seat bind = %+v, want all granted capabilities", bind)
	}
	callback := readEIRequest(t, server)
	if callback.object != connID || callback.opcode != 0 || len(callback.body) < 8 {
		t.Fatalf("unexpected callback: %+v", callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])

	stream = eiRequest(seatID, 4, append(eiU64(devID), eiU32(1)...))
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(devID, 5, append(eiU64(eiServerIDFloor+101+uint64(i)), eiString(name)...))...)
	}
	region := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(1920)...), eiU32(1080)...), eiF32(1)...)
	stream = append(stream, eiRequest(devID, 4, region)...)
	stream = append(stream, eiRequest(devID, 6, nil)...)
	stream = append(stream, eiRequest(devID, 7, eiU32(9))...)
	stream = append(stream, eiRequest(callbackID, 0, eiU64(0))...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	var sender *eiSender
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("setupEISender: %v", result.err)
		}
		sender = result.sender
	case <-ctx.Done():
		t.Fatalf("EIS negotiation timed out: %v", ctx.Err())
	}
	defer sender.client.close()
	start := readEIRequest(t, server)
	if start.object != devID || start.opcode != 1 {
		t.Fatalf("missing emulation start: %+v", start)
	}
	if !sender.validatePoint(100, 200) || sender.validatePoint(1920, 1080) {
		t.Fatal("authorized EIS region was not enforced")
	}

	portal := &portalSession{}
	backend := newRemoteDesktopBackend(sender, portal)
	defer backend.Close()
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("press authorized key: %v", err)
	}
	key := readEIRequest(t, server)
	frame := readEIRequest(t, server)
	if key.object != eiServerIDFloor+103 || key.opcode != 1 || frame.object != devID || frame.opcode != 3 {
		t.Fatalf("key press not routed through granted interface: %+v, %+v", key, frame)
	}
	if err := backend.MouseMove(ctx, 100, 200); err != nil {
		t.Fatalf("move authorized pointer: %v", err)
	}
	pointer := readEIRequest(t, server)
	frame = readEIRequest(t, server)
	if pointer.object != eiServerIDFloor+101 || pointer.opcode != 1 || frame.object != devID {
		t.Fatalf("pointer event: %+v, %+v", pointer, frame)
	}
	if err := backend.MouseMove(ctx, 1920, 1080); err == nil {
		t.Fatal("allowed input outside authorized region")
	}
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatalf("mouse down: %v", err)
	}
	button := readEIRequest(t, server)
	frame = readEIRequest(t, server)
	if button.object != eiServerIDFloor+102 || button.opcode != 1 || frame.object != devID {
		t.Fatalf("button event: %+v, %+v", button, frame)
	}
	// A paused authorized device revokes all subsequent input operations.
	if _, err := server.Write(eiRequest(devID, 8, eiU32(10))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.revoked:
		if err := backend.KeyUp(ctx, "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("post-revocation key up error: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("device revocation not observed: %v", ctx.Err())
	}
}

type eisSetupResult struct {
	sender *eiSender
	err    error
}

func startEISenderWithResult(t *testing.T) (*net.UnixConn, context.Context, context.CancelFunc, <-chan eisSetupResult) {
	t.Helper()
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	done := make(chan eisSetupResult, 1)
	go func() { sender, err := setupEISender(ctx, client); done <- eisSetupResult{sender: sender, err: err} }()
	return server, ctx, cancel, done
}

// observedEISWaitContext reports when the EIS reader has subscribed to
// cancellation. This makes a blocked-wait test independent of scheduler timing.
type observedEISWaitContext struct { //nolint:containedctx // Test wrapper observes when Done is subscribed without altering cancellation semantics.
	parent  context.Context //nolint:containedctx // Observes the real context, never stored in production.
	started chan struct{}
	once    sync.Once
}

func (c *observedEISWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.parent.Done()
}

func (c *observedEISWaitContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *observedEISWaitContext) Err() error                  { return c.parent.Err() }
func (c *observedEISWaitContext) Value(key any) any           { return c.parent.Value(key) }

func TestEIClientBlockedNextHonorsCancellationWithoutConsumingSerial(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(clientConn)
	defer client.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
	result := make(chan error, 1)
	client.serial.Store(71)
	go func() { _, err := client.next(wait); result <- err }()
	select {
	case <-wait.started:
	case <-time.After(time.Second):
		t.Fatal("EIS next did not enter cancelable wait")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled EIS wait returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("EIS wait ignored caller cancellation")
	}
	if got := client.serial.Load(); got != 71 {
		t.Fatalf("cancelled wait advanced event serial to %d", got)
	}
}

func TestEIClientBlockedNextReportsRemoteTransportFailure(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	client := newEIClient(clientConn)
	defer client.close()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := client.next(wait); result <- err }()
	select {
	case <-wait.started:
	case <-ctx.Done():
		t.Fatal("EIS reader never entered event wait")
	}
	if err := peer.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, io.EOF) {
			t.Fatalf("EIS wait lost peer EOF error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("EIS reader did not report peer half-close")
	}
}

func TestEIClientWriteBatchStopsAtClosedTransport(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	defer peer.Close()
	defer clientConn.Close()
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	c := &eiClient{conn: clientConn}
	err := c.writeMessages([]byte{1, 2, 3}, []byte{4, 5, 6})
	if !errors.Is(err, unix.EPIPE) {
		t.Fatalf("EIS writes accepted a closed output stream: %v", err)
	}
}

func TestEIClientNeverDeliversQueuedMessagesAfterCallerCanceled(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
		c.messages <- eiMessage{object: 17, opcode: 3, body: eiU32(42)}
		msg, err := c.next(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: canceled client delivered queued event %+v: %v", i, msg, err)
		}
	}
}

func TestEIReceivedFileDescriptorCleanupDoesNotLeakOrCloseUnrelatedDescriptors(t *testing.T) {
	// The client must discard and close all SCM_RIGHTS descriptors from an
	// untrusted peer. A descriptor is transferred to the reader, not shared.
	fds := make([]int, 2)
	if err := unix.Pipe(fds); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	incoming := make([]int, 4)
	for i := range incoming {
		dup, err := unix.Dup(fds[0])
		if err != nil {
			t.Fatal(err)
		}
		incoming[i] = dup
	}
	control := unix.UnixRights(incoming...)
	closeReceivedFDs(control)
	for _, fd := range incoming {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Errorf("received descriptor %d was not closed: %v", fd, err)
			_ = unix.Close(fd)
		}
	}
	if _, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFD, 0); err != nil {
		t.Fatalf("cleanup closed unrelated original descriptor: %v", err)
	}
	closeReceivedFDs([]byte{0, 1, 2})
}

// An already-retired Unix transport must produce a consistent result for
// every owner that races to close the same EIS client.
func TestEIClientCloseReturnsSameTransportFailureToEveryCaller(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	c := &eiClient{conn: conn, stop: make(chan struct{})}
	const owners = 12
	result := make(chan error, owners)
	var wg sync.WaitGroup
	for range owners {
		wg.Add(1)
		go func() { defer wg.Done(); result <- c.close() }()
	}
	wg.Wait()
	close(result)
	for err := range result {
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("close did not retain the transport failure: %v", err)
		}
	}
	select {
	case <-c.stop:
	default:
		t.Fatal("EIS client shutdown signal was not published")
	}
}

// Client-allocated callback IDs must never alias server-owned connection,
// seat, device, or interface objects, even after counter exhaustion.
func TestEIClientObjectIDsStayInsideClientNamespace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start uint64
		want  uint64
	}{
		{"initial client callback", 1, 1},
		{"last client callback", eiServerIDFloor - 1, eiServerIDFloor - 1},
		{"at server boundary", eiServerIDFloor, 0},
		{"past server boundary", eiServerIDFloor + 17, 0},
		{"counter wrapped", 0, 0},
		{"maximum uint64", ^uint64(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &eiClient{}
			c.clientID.Store(tc.start)
			got := c.nextClientObjectID()
			if got != tc.want {
				t.Fatalf("counter %x allocated callback %x, want %x", tc.start, got, tc.want)
			}
			if tc.want == eiServerIDFloor-1 {
				if next := c.nextClientObjectID(); next != 0 {
					t.Fatalf("exhausted client namespace allocated object %x", next)
				}
			}
		})
	}
}

func TestEIClientObjectIDAllocationRemainsUniqueAcrossConcurrentCallers(t *testing.T) {
	c := &eiClient{}
	c.clientID.Store(eiServerIDFloor - 8)
	const workers = 16
	ids := make(chan uint64, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() { defer wg.Done(); ids <- c.nextClientObjectID() }()
	}
	wg.Wait()
	close(ids)
	seen := make(map[uint64]bool)
	for id := range ids {
		if id == 0 {
			continue
		}
		if id < eiServerIDFloor-8 || id >= eiServerIDFloor || seen[id] {
			t.Fatalf("invalid or duplicate client object allocation: %x", id)
		}
		seen[id] = true
	}
	if len(seen) != 8 {
		t.Fatalf("allocated %d of 8 remaining distinct client IDs", len(seen))
	}
}

// Closing a server event channel is terminal: it is not a valid object-zero
// protocol event and must not be interpreted as successful input state.
func TestEISNextRejectsClosedMessageStreamWithoutAdvancingSerial(t *testing.T) {
	c := &eiClient{messages: make(chan eiMessage), done: make(chan struct{})}
	c.serial.Store(77)
	close(c.messages)
	message, err := c.next(context.Background())
	if err == nil || message.object != 0 || message.opcode != 0 || c.serial.Load() != 77 {
		t.Fatalf("closed event stream accepted: event=%+v err=%v serial=%d", message, err, c.serial.Load())
	}
}

func TestRemoteDesktopRevokesSessionWhenEISMessagesCloseUnexpectedly(t *testing.T) {
	b, _, c := newSyntheticRevocableRemoteDesktop(t)
	close(c.messages)
	select {
	case <-b.revoked:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("closed EIS event stream left authorized input live")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("closed EIS messages kept session valid: %v", err)
	}
}

// ei_device.done seals initial enumeration. A compositor cannot add new
// authoritative capability objects or regions to an already completed grant.
func TestEISCompletedDeviceRejectsLateCapabilityAnnouncements(t *testing.T) {
	const deviceID = eiServerIDFloor + 42
	for _, tc := range []struct {
		name   string
		opcode uint32
		body   []byte
	}{{"interface", 5, append(eiU64(eiServerIDFloor+55), eiString("ei_keyboard")...)}, {"region", 4, append(append(append(append(eiU32(0), eiU32(0)...), eiU32(100)...), eiU32(100)...), eiF32(1)...)}} {
		t.Run(tc.name, func(t *testing.T) {
			device := &eiDevice{id: deviceID, version: 1, done: true, resumed: true, interfaces: map[string]uint64{}}
			sender := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: tc.opcode, body: tc.body})
			if err == nil || !strings.Contains(err.Error(), "completed") {
				t.Fatalf("accepted late %s after device.done: %v", tc.name, err)
			}
			if len(device.interfaces) != 0 || len(device.regions) != 0 {
				t.Fatalf("late %s mutated completed grant: %+v", tc.name, device)
			}
		})
	}
}

// EIS object IDs identify protocol objects across the entire connection,
// not just within a single device's interface map. A malformed second device
// must not reuse an already authorized interface object ID.
func TestEISRejectsInterfaceObjectReusedAcrossDevices(t *testing.T) {
	const firstID = eiServerIDFloor + 21
	const secondID = eiServerIDFloor + 22
	first := &eiDevice{id: firstID, version: 1, interfaces: map[string]uint64{"ei_keyboard": eiServerIDFloor + 101}}
	second := &eiDevice{id: secondID, version: 1, interfaces: map[string]uint64{}}
	sender := &eiSender{devices: map[uint64]*eiDevice{firstID: first, secondID: second}}
	for _, name := range []string{"ei_keyboard", "ei_button", "ei_pointer_absolute", "ei_text"} {
		err := sender.consumeSetupEvent(eiMessage{object: secondID, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString(name)...)})
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("cross-device object alias %q accepted: %v", name, err)
		}
		if len(second.interfaces) != 0 || first.interfaces["ei_keyboard"] != eiServerIDFloor+101 {
			t.Errorf("alias corrupted authorization: first=%v second=%v", first.interfaces, second.interfaces)
		}
	}
	if err := sender.consumeSetupEvent(eiMessage{object: secondID, opcode: 5, body: append(eiU64(eiServerIDFloor+102), eiString("ei_button")...)}); err != nil {
		t.Fatalf("distinct second-device interface rejected: %v", err)
	}
}

type eisDequeueBoundaryContext struct {
	parent        context.Context
	onSecondCheck func()
	checks        int
}

func (c *eisDequeueBoundaryContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *eisDequeueBoundaryContext) Done() <-chan struct{}       { return c.parent.Done() }
func (c *eisDequeueBoundaryContext) Value(key any) any           { return c.parent.Value(key) }
func (c *eisDequeueBoundaryContext) Err() error {
	c.checks++
	if c.checks == 2 && c.onSecondCheck != nil {
		c.onSecondCheck()
	}
	return c.parent.Err()
}

// A queued EIS event may be selected at exactly the moment the caller loses
// authority. Model the transition at the second cancellation check to make the
// race deterministic: the client must not publish that event or advance serial.
func TestEISNextRejectsCancellationImmediatelyAfterDequeuingEvent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &eisDequeueBoundaryContext{parent: parent, onSecondCheck: cancel}
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.serial.Store(73)
	c.messages <- eiMessage{object: eiServerIDFloor + 9, opcode: 7, body: eiU32(999)}
	event, err := c.next(ctx)
	if !errors.Is(err, context.Canceled) || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("canceled receive returned event=%+v err=%v", event, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("revoked event advanced serial to %d", got)
	}
	if ctx.checks != 2 {
		t.Fatalf("unexpected cancellation checks: %d", ctx.checks)
	}
}

// The socket can terminate after the first preflight but before the selected
// event has been delivered. The post-dequeue terminal check must take priority
// over a buffered frame, even if the caller's own context is still active.
func TestEISNextRejectsTransportFailureImmediatelyAfterDequeuingEvent(t *testing.T) {
	failure := errors.New("synthetic peer revoked during dequeue")
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.serial.Store(73)
	c.messages <- eiMessage{object: eiServerIDFloor + 9, opcode: 7, body: eiU32(999)}
	ctx := &eisDequeueBoundaryContext{parent: context.Background(), onSecondCheck: func() { c.setReadError(failure); close(c.done) }}
	event, err := c.next(ctx)
	if !errors.Is(err, failure) || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("terminal receive returned event=%+v err=%v", event, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("revoked event advanced serial to %d", got)
	}
	if ctx.checks != 2 {
		t.Fatalf("unexpected cancellation checks: %d", ctx.checks)
	}
}

// EIS devices have independent interface objects, but a device containing
// multiple interfaces must receive just one emulation-start request. Optional
// absent devices are not authorized targets and must not receive any request.
func TestEISDeviceActivationStartsEachAuthorizedDeviceOnceOnWire(t *testing.T) {
	local, remote := unixConnectionPair(t)
	defer remote.Close()
	if err := remote.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client := &eiClient{conn: local}
	client.serial.Store(0x10203040)
	sender := &eiSender{
		client:      client,
		keyboardDev: eiServerIDFloor + 0x10,
		buttonDev:   eiServerIDFloor + 0x10,
		scrollDev:   0,
		textDev:     0,
		pointers: []eiPointerDevice{
			{device: eiServerIDFloor + 0x20, pointer: eiServerIDFloor + 0x21},
			{device: eiServerIDFloor + 0x20, pointer: eiServerIDFloor + 0x21},
		},
	}
	if err := sender.selectDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []uint64{sender.keyboardDev, sender.pointers[0].device} {
		msg, err := readSyntheticEISFrame(remote)
		if err != nil {
			t.Fatalf("read authorized device start %x: %v", want, err)
		}
		if msg.object != want || msg.opcode != 1 || len(msg.body) != 8 || binary.NativeEndian.Uint32(msg.body[:4]) != 0x10203040 || binary.NativeEndian.Uint32(msg.body[4:]) != 1 {
			t.Fatalf("unexpected emulation activation of device %x: %+v", want, msg)
		}
	}
	if msg, err := readSyntheticEISFrame(remote); !errors.Is(err, io.EOF) {
		t.Fatalf("duplicate, absent, or unauthorized device was started: %+v err=%v", msg, err)
	}
}

// Cancellation and a broken EIS transport must terminate activation instead
// of allowing a partially configured input session to appear authorized.
func TestEISDeviceActivationRejectsCanceledAndBrokenSocket(t *testing.T) {
	t.Run("canceled before first write", func(t *testing.T) {
		local, remote := unixConnectionPair(t)
		defer remote.Close()
		defer local.Close()
		if err := remote.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		sender := &eiSender{client: &eiClient{conn: local}, keyboardDev: eiServerIDFloor + 0x10, buttonDev: eiServerIDFloor + 0x20}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sender.selectDevices(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled selection = %v", err)
		}
		if err := local.Close(); err != nil {
			t.Fatal(err)
		}
		if msg, err := readSyntheticEISFrame(remote); !errors.Is(err, io.EOF) {
			t.Fatalf("canceled activation emitted frame %+v, err=%v", msg, err)
		}
	})
	t.Run("peer disconnect before activation", func(t *testing.T) {
		local, remote := unixConnectionPair(t)
		defer local.Close()
		if err := remote.Close(); err != nil {
			t.Fatal(err)
		}
		sender := &eiSender{client: &eiClient{conn: local}, keyboardDev: eiServerIDFloor + 0x10, buttonDev: eiServerIDFloor + 0x20}
		if err := sender.selectDevices(context.Background()); err == nil || !strings.Contains(err.Error(), "start device emulation") {
			t.Fatalf("lost peer was accepted as authorized EIS activation: %v", err)
		}
	})
}

// Seat capabilities arrive over an untrusted protocol socket. The client must
// reject malformed masks and interface names before binding a seat or starting
// any emulated input device; revocation while capabilities are arriving is
// terminal, rather than an ignorable event from a different object.
func TestEISSeatNegotiationRejectsMalformedCapabilitiesAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packet  func(connection, seat uint64) []byte
		want    string
		revoked bool
	}{
		{"truncated capability mask", func(_, seat uint64) []byte { return eiRequest(seat, 2, nil) }, "input/eis", false},
		{"missing capability name", func(_, seat uint64) []byte { return eiRequest(seat, 2, eiU64(1)) }, "input/eis", false},
		{"invalid capability utf8", func(_, seat uint64) []byte {
			return eiRequest(seat, 2, append(eiU64(1), eiString(string([]byte{0xff}))...))
		}, "invalid UTF-8", false},
		{"connection revoked during capability negotiation", func(connection, _ uint64) []byte { return eiRequest(connection, 0, nil) }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, done := startEISenderSetup(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const connection = eiServerIDFloor + 0x71
			const seat = eiServerIDFloor + 0x72
			connectionBody := append(append(eiU32(6), eiU64(connection)...), eiU32(1)...)
			stream := eiRequest(0, 2, connectionBody)
			stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
			stream = append(stream, tc.packet(connection, seat)...)
			if _, err := server.Write(stream); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if tc.revoked {
					if !errors.Is(err, ErrRemoteDesktopRevoked) {
						t.Fatalf("revoked seat still authorized: %v", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("malformed seat capability accepted, error=%v, want %q", err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("failed to reject malformed seat before deadline: %v", ctx.Err())
			}
		})
	}
}

// A resumed device may still be enumerating its protocol interfaces or
// regions. A sender must not begin injecting input on an incomplete EIS
// device before the ei_device.done event seals its advertised capabilities.
func TestEISDeviceReadinessWaitsForDoneOnAllSelectedDevices(t *testing.T) {
	keyboard := &eiDevice{id: eiServerIDFloor + 0x20, version: 1, resumed: true, done: true, interfaces: map[string]uint64{"ei_keyboard": 101, "ei_button": 102}}
	pointer := &eiDevice{id: eiServerIDFloor + 0x21, version: 1, resumed: true, done: false, interfaces: map[string]uint64{"ei_pointer_absolute": 103}, regions: []eiRegion{{x: 0, y: 0, width: 200, height: 100, scale: 1}}}
	sender := &eiSender{devices: map[uint64]*eiDevice{keyboard.id: keyboard, pointer.id: pointer}}
	if sender.devicesReady() {
		t.Fatal("sender accepted resumed pointer before its device.done")
	}
	if err := sender.consumeSetupEvent(eiMessage{object: pointer.id, opcode: 6}); err != nil {
		t.Fatal(err)
	}
	if !sender.devicesReady() {
		t.Fatal("completed keyboard and pointer should be ready")
	}
	keyboard.done = false
	if sender.devicesReady() {
		t.Fatal("sender accepted incomplete keyboard before its device.done")
	}
	keyboard.done = true
	if !sender.devicesReady() {
		t.Fatal("completed keyboard and pointer should remain ready")
	}
}

// The server must not announce a device with an ID already assigned to the
// connection's seat, connection, or an authorized interface object.
func TestEISRejectsDeviceObjectIdentityCollisions(t *testing.T) {
	const seat = eiServerIDFloor + 11
	const connection = eiServerIDFloor + 10
	const device = eiServerIDFloor + 12
	const interfaceID = eiServerIDFloor + 30
	for _, tc := range []struct {
		name string
		id   uint64
	}{{"seat", seat}, {"connection", connection}, {"authorized interface", interfaceID}} {
		t.Run(tc.name, func(t *testing.T) {
			existing := &eiDevice{id: device, version: 1, interfaces: map[string]uint64{"ei_keyboard": interfaceID}}
			sender := &eiSender{seat: seat, connection: connection, devices: map[uint64]*eiDevice{device: existing}}
			err := sender.consumeSetupEvent(eiMessage{object: seat, opcode: 4, body: append(eiU64(tc.id), eiU32(1)...)})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("colliding device %s accepted: %v", tc.name, err)
			}
			if len(sender.devices) != 1 || sender.devices[device] != existing {
				t.Fatalf("device collision changed previous authority: %v", sender.devices)
			}
		})
	}
}

func TestEISRejectsInterfaceIdentityMatchingDeviceSeatOrConnection(t *testing.T) {
	const seat = eiServerIDFloor + 11
	const connection = eiServerIDFloor + 10
	const device = eiServerIDFloor + 12
	for _, tc := range []struct {
		name string
		id   uint64
	}{{"seat", seat}, {"connection", connection}, {"device", device}} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: seat, connection: connection, devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: map[string]uint64{}}}}
			err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(tc.id), eiString("ei_keyboard")...)})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("colliding interface %s accepted: %v", tc.name, err)
			}
			if len(sender.devices[device].interfaces) != 0 {
				t.Fatalf("collision granted keyboard authority: %v", sender.devices[device].interfaces)
			}
		})
	}
}

// An EIS device that disappears during setup no longer authorizes any of
// its advertised input capabilities. Setup must fail closed rather than
// retaining a previously completed and resumed device.
func TestEISDeviceRemovalDuringSetupRevokesWithoutPublishingSender(t *testing.T) {
	server, ctx, done := prepareEISInvalidGrantEndpoint(t)
	const seat = eiServerIDFloor + 0x11
	const device = eiServerIDFloor + 0x12
	stream := eiRequest(seat, 4, append(eiU64(device), eiU32(1)...))
	stream = append(stream, eiRequest(device, 5, append(eiU64(eiServerIDFloor+0x31), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(device, 6, nil)...)
	stream = append(stream, eiRequest(device, 7, eiU32(1))...)
	stream = append(stream, eiRequest(device, 0, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("destroyed device retained authorization: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("destroyed device left startup waiting: %v", ctx.Err())
	}
}

// Re-announcing an existing device ID is a protocol replay, not an update.
// Replacing its descriptor drops validated interfaces, regions and done/
// resume state, potentially corrupting negotiated input authorization.
func TestEISRepeatedDeviceAnnouncementMustNotReplaceAuthorizedDevice(t *testing.T) {
	const seat, device = eiServerIDFloor + 11, eiServerIDFloor + 21
	initial := &eiDevice{id: device, version: 1, resumed: true, done: true, interfaces: map[string]uint64{"ei_keyboard": eiServerIDFloor + 30, "ei_pointer_absolute": eiServerIDFloor + 31}, regions: []eiRegion{{x: 0, y: 0, width: 120, height: 80, scale: 1}}}
	for _, version := range []uint32{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			sender := &eiSender{seat: seat, devices: map[uint64]*eiDevice{device: initial}}
			replay := eiMessage{object: seat, opcode: 4, body: append(eiU64(device), eiU32(version)...)}
			if err := sender.consumeSetupEvent(replay); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate device ID %d version %d accepted: %v", device, version, err)
			}
			if sender.devices[device] != initial || !sender.devices[device].done || !sender.devices[device].resumed {
				t.Fatalf("existing authorized device replaced: %+v", sender.devices[device])
			}
		})
	}
}

// The client announces support only for ei_device version 1. The compositor
// cannot grant a device at a newer unsupported protocol version.
func TestEISRejectsUnnegotiatedDeviceVersionOverWire(t *testing.T) {
	for _, version := range []uint32{2, 3, ^uint32(0)} {
		t.Run(string(rune('A'+version%26)), func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			const seat = eiServerIDFloor + 0x11
			const device = eiServerIDFloor + 0x12
			if _, err := server.Write(eiRequest(seat, 4, append(eiU64(device), eiU32(version)...))); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device") {
					t.Fatalf("unnegotiated device version %d accepted: sender=%v err=%v", version, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("unnegotiated device version %d left setup awaiting devices", version)
			}
		})
	}
}

// An ei_connection.disconnect is a terminal lifecycle event whose first
// payload word is a reason code, not an input-device event serial. It must
// not replace the last negotiated serial as next drains pending events.
func TestEIConnectionDisconnectReasonCannotReplaceAuthorizedSerial(t *testing.T) {
	const connection = eiServerIDFloor + 21
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.connection.Store(connection)
	c.serial.Store(73)
	c.messages <- eiMessage{object: connection, opcode: 0, body: eiU32(0xdeadbeef)}
	msg, err := c.next(context.Background())
	if err != nil || msg.object != connection || msg.opcode != 0 {
		t.Fatalf("lost lifecycle event: %+v %v", msg, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("disconnect reason corrupted negotiated serial: %d", got)
	}
}

func TestEIConnectionSerialStillAdvancesFromHandshake(t *testing.T) {
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.messages <- eiMessage{object: 0, opcode: 2, body: append(append(eiU32(91), eiU64(eiServerIDFloor+10)...), eiU32(1)...)}
	if _, err := c.next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.serial.Load(); got != 91 {
		t.Fatalf("handshake serial was lost: %d", got)
	}
}

// The ei_handshake.connection event establishes the authoritative server-side
// ei_connection object exactly once. A second announcement must not replace
// it after negotiation: otherwise an attacker-controlled peer can rebind the
// handler that acknowledges pings and controls subsequent transport state.
func TestEISDuplicateHandshakeConnectionCannotReplaceNegotiatedObject(t *testing.T) {
	for _, tc := range []struct {
		name       string
		replayedID uint64
	}{
		{name: "duplicate same connection", replayedID: eiServerIDFloor + 17},
		{name: "rebind different connection", replayedID: eiServerIDFloor + 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			const authoritativeID = eiServerIDFloor + 17
			first := eiRequest(0, 2, append(append(eiU32(1), eiU64(authoritativeID)...), eiU32(1)...))
			if _, err := serverConn.Write(first); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.next(ctx); err != nil {
				t.Fatal(err)
			}
			if got := client.connection.Load(); got != authoritativeID {
				t.Fatalf("initial connection %d, want %d", got, authoritativeID)
			}
			replay := eiRequest(0, 2, append(append(eiU32(2), eiU64(tc.replayedID)...), eiU32(1)...))
			if _, err := serverConn.Write(replay); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "duplicate") {
					t.Fatalf("duplicate handshake not rejected: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("duplicate handshake did not terminate EIS stream: %v", ctx.Err())
			}
			if got := client.connection.Load(); got != authoritativeID {
				t.Fatalf("duplicate handshake replaced live authority ID with %d", got)
			}
		})
	}
}

// A device's announced interface name is an immutable protocol binding.
// An unsolicited repeated or conflicting advertisement must not replace an
// already selected keyboard, button, pointer, scroll or text object.
func TestEISDeviceInterfaceCannotBeReboundByRepeatedAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement uint64
	}{
		{"different interface object", eiServerIDFloor + 102},
		{"replayed interface object", eiServerIDFloor + 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{eiServerIDFloor + 12: {id: eiServerIDFloor + 12, version: 1, interfaces: map[string]uint64{}}}}
			device := sender.devices[eiServerIDFloor+12]
			first := eiMessage{object: device.id, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...)}
			if err := sender.consumeSetupEvent(first); err != nil {
				t.Fatalf("initial keyboard authorization rejected: %v", err)
			}
			replay := eiMessage{object: device.id, opcode: 5, body: append(eiU64(tc.replacement), eiString("ei_keyboard")...)}
			err := sender.consumeSetupEvent(replay)
			if err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("repeated interface announcement accepted: %v", err)
			}
			if device.interfaces["ei_keyboard"] != eiServerIDFloor+101 || len(device.interfaces) != 1 {
				t.Fatalf("replayed interface changed keyboard authority: %+v", device.interfaces)
			}
		})
	}
}

func TestEISRejectsZeroIdentityDeviceAndInterfaceAnnouncements(t *testing.T) {
	device := eiServerIDFloor + 12
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"zero device", append(eiU64(0), eiU32(1)...)},
		{"zero device version", append(eiU64(device), eiU32(0)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: eiServerIDFloor + 11, devices: make(map[uint64]*eiDevice)}
			err := sender.consumeSetupEvent(eiMessage{object: sender.seat, opcode: 4, body: tc.body})
			if err == nil || len(sender.devices) != 0 {
				t.Fatalf("invalid device identity admitted: %v devices=%v", err, sender.devices)
			}
		})
	}
	sender := &eiSender{devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(0), eiString("ei_keyboard")...)})
	if err == nil || len(sender.devices[device].interfaces) != 0 {
		t.Fatalf("zero interface object admitted: %v interfaces=%v", err, sender.devices[device].interfaces)
	}
}

// The synthetic compositor communicates over the same Unix wire protocol as
// the actual EIS server. A forged interface rebind during enumeration must
// abort setup before any device becomes available for input injection.
func TestEISHandshakeRejectsReboundOrZeroIdentityInterfacesOverRealUnixSocket(t *testing.T) {
	for _, tc := range []struct {
		name       string
		additional []byte
		want       string
	}{
		{"different keyboard object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(eiServerIDFloor+102), eiString("ei_keyboard")...)), "duplicate interface"},
		{"replayed keyboard object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...)), "duplicate interface"},
		{"zero interface object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(0), eiString("ei_button")...)), "invalid interface"},
		{"zero device version", eiRequest(eiServerIDFloor+0x11, 4, append(eiU64(eiServerIDFloor+0x12), eiU32(0)...)), "invalid device"},
		{"zero device object", eiRequest(eiServerIDFloor+0x11, 4, append(eiU64(0), eiU32(1)...)), "invalid device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertEISWireRejectsInvalidAuthorization(t, tc.additional, tc.want)
		})
	}
}

func assertEISWireRejectsInvalidAuthorization(t *testing.T, additional []byte, want string) {
	t.Helper()
	server, ctx, done := prepareEISInvalidGrantEndpoint(t)
	const seat = eiServerIDFloor + 0x11
	const device = eiServerIDFloor + 0x12
	if want == "duplicate interface" || want == "invalid interface" {
		if _, err := server.Write(eiRequest(seat, 4, append(eiU64(device), eiU32(1)...))); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Write(eiRequest(device, 5, append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...))); err != nil {
			t.Fatal(err)
		}
	}
	// No end-of-enumeration callback is needed: a malformed input grant
	// must terminate setup immediately, before publishing any sender.
	if _, err := server.Write(additional); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), want) {
			t.Fatalf("forged EIS authority accepted: sender=%v err=%v", result.sender, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("invalid EIS grant not rejected promptly: %v", ctx.Err())
	}
}

func prepareEISInvalidGrantEndpoint(t *testing.T) (*net.UnixConn, context.Context, <-chan eisSetupResult) {
	t.Helper()
	server, ctx, cancel, done := startEISenderWithResult(t)
	t.Cleanup(cancel)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	const seat = eiServerIDFloor + 0x11
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		stream = append(stream, eiRequest(seat, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seat, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	callback := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 || callback.object != connection || callback.opcode != 0 || len(callback.body) < 8 {
		t.Fatalf("unexpected EIS negotiation: bind=%+v callback=%+v", bind, callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])
	if callbackID == 0 {
		t.Fatal("negotiation selected a zero callback identity")
	}
	return server, ctx, done
}

// An object-zero seat is not an authorized ei_seat. Accepting it can send
// subsequent seat-binding requests on the handshake object instead.
func TestEISHandshakeRejectsZeroSeatWithoutBindingAnyCapabilities(t *testing.T) {
	server, ctx, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(0))...)
	// Explicit zero-seat completion triggers the old path to accept a
	// handshake-object capability response instead of rejecting the seat.
	stream = append(stream, eiRequest(0, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
			t.Fatalf("zero EIS seat was not rejected at identity boundary: sender=%v err=%v", result.sender, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("zero seat never rejected: %v", ctx.Err())
	}
}

// ConnectToEIS is a byte stream. A zero-length datagram carrying a descriptor
// must terminate the reader and close the unsolicited descriptor instead of
// leaving the event loop spinning on an empty packet.
func TestEIReadLoopRejectsEmptyDatagramAndClosesTransferredDescriptor(t *testing.T) {
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	peer := sockets[1]
	t.Cleanup(func() { _ = unix.Close(peer) })

	file := os.NewFile(uintptr(sockets[0]), "eis-datagram")
	if file == nil {
		t.Fatal("wrap EIS socket pair endpoint")
	}
	conn, err := net.FileConn(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close original EIS descriptor: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("wrap EIS datagram socket: %v", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		t.Fatalf("wrapped EIS socket has type %T, want *net.UnixConn", conn)
	}
	client := newEIClient(unixConn)
	t.Cleanup(func() { _ = client.close() })

	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(pipe[0]) })
	t.Cleanup(func() { _ = unix.Close(pipe[1]) })
	if n, err := unix.SendmsgN(peer, nil, unix.UnixRights(pipe[1]), nil, 0); err != nil || n != 0 {
		t.Fatalf("send zero-payload descriptor packet: bytes=%d err=%v", n, err)
	}

	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("empty EIS datagram did not terminate the reader")
	}
	if err := client.readError(); err == nil || !strings.Contains(err.Error(), "peer closed the EIS stream") {
		t.Fatalf("empty EIS datagram error = %v", err)
	}
	if err := unix.Close(pipe[1]); err != nil {
		t.Fatal(err)
	}
	pipe[1] = -1
	var data [1]byte
	if n, err := unix.Read(pipe[0], data[:]); err != nil || n != 0 {
		t.Fatalf("reader retained transferred descriptor: read = (%d, %v), want EOF", n, err)
	}
}

// A compositor must not reserve an interface protocol object under an empty
// name, since it cannot represent any EIS capability and can block a later
// authorized interface from using its object ID.
func TestEISRejectsUnnamedInterfaceWithoutReservingItsObject(t *testing.T) {
	const device = eiServerIDFloor + 31
	const object = eiServerIDFloor + 45
	sender := &eiSender{devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(object), eiString("")...)})
	if err == nil || !strings.Contains(err.Error(), "interface") {
		t.Fatalf("unnamed EIS interface accepted: %v", err)
	}
	if len(sender.devices[device].interfaces) != 0 || sender.isReservedEISObject(object) {
		t.Fatalf("rejected interface reserved identity: %v", sender.devices[device].interfaces)
	}
	if err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(object), eiString("ei_keyboard")...)}); err != nil {
		t.Fatalf("valid interface not accepted after malformed one: %v", err)
	}
}

func TestAuthorizedEISEmptyTypeDoesNotEmitFrames(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.Type(ctx, ""); err != nil {
		t.Fatalf("empty key sequence: %v", err)
	}
	if err := backend.TypeLiteral(ctx, "probe"); err != nil {
		t.Fatalf("authorized follow-up text: %v", err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+105, 2)
	if len(backend.heldKeys) != 0 {
		t.Fatalf("empty key sequence changed held keys: %v", backend.heldKeys)
	}
}

// A slow event consumer can fill the reader's bounded queue. Closing the
// transport must always terminate the reader without waiting for the caller
// to drain or accept an event from a session that no longer exists.
func TestEISReaderShutdownWithFullIncomingEventQueue(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const count = 160
	payload := make([]byte, 0, count*20)
	for index := uint32(0); index < count; index++ {
		payload = append(payload, eiRequest(eiServerIDFloor+7, 5, eiU32(index+1))...)
	}
	if _, err := serverConn.Write(payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for len(client.messages) != cap(client.messages) {
		select {
		case <-client.done:
			t.Fatalf("reader terminated before queue filled: %v", client.readError())
		case <-deadline:
			t.Fatalf("queue failed to fill: buffered %d/%d", len(client.messages), cap(client.messages))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := client.close(); err != nil {
		t.Fatalf("shutdown full EIS queue: %v", err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on a full queue after shutdown")
	}
	if client.serial.Load() != 0 {
		t.Fatalf("reader advanced serial without consumer authorization: %d", client.serial.Load())
	}
	event, err := client.next(context.Background())
	if err == nil || !errors.Is(err, client.readError()) && client.readError() != nil || event.object != 0 {
		t.Fatalf("terminal reader exposed queued event: %+v %v (read error %v)", event, err, client.readError())
	}
}

// cancelAfterEISWaitContext cancels after the selected setup message has been
// removed from the EIS stream and validated by eiClient.next.
type cancelAfterEISWaitContext struct {
	context.Context
	cancel    context.CancelFunc
	waits     atomic.Uint32
	errCalls  atomic.Uint32
	cancelErr uint32
	cancelAt  uint32
	cancelled atomic.Bool
}

func (c *cancelAfterEISWaitContext) Done() <-chan struct{} {
	c.waits.Add(1)
	return c.Context.Done()
}

func (c *cancelAfterEISWaitContext) Err() error {
	err := c.Context.Err()
	errCall := c.errCalls.Add(1)
	triggered := (c.cancelAt != 0 && c.waits.Load() == c.cancelAt) || (c.cancelErr != 0 && errCall == c.cancelErr)
	if triggered && err == nil && c.cancelled.CompareAndSwap(false, true) {
		c.cancel()
	}
	return err
}

func TestEISenderDoesNotOutliveCancellationAfterFinalReadinessEvent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelAtWait uint32
		cancelAtErr  uint32
		wantStarted  bool
	}{
		{name: "after final readiness event", cancelAtWait: 19},
		{name: "before device activation", cancelAtErr: 39},
		{name: "during device activation", cancelAtErr: 40, wantStarted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			t.Cleanup(func() { _ = serverConn.Close() })
			base, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			// The synthetic peer sends 19 setup events, ending with the successful
			// synchronization callback after all input devices have resumed.
			ctx := &cancelAfterEISWaitContext{
				Context: base, cancel: cancel, cancelAt: tc.cancelAtWait, cancelErr: tc.cancelAtErr,
			}
			peerDone := make(chan error, 1)
			go func() { peerDone <- serveSyntheticAuthorizedEISPeer(serverConn) }()

			sender, err := setupEISender(ctx, clientConn)
			_ = clientConn.Close()
			if got := ctx.waits.Load(); got != 19 || !ctx.cancelled.Load() {
				t.Fatalf("setup canceled after wait %d (triggered=%v), want final readiness wait 19", got, ctx.cancelled.Load())
			}
			select {
			case peerErr := <-peerDone:
				if tc.wantStarted {
					if peerErr != nil {
						t.Fatalf("synthetic EIS peer did not observe device activation: %v", peerErr)
					}
				} else if !errors.Is(peerErr, io.EOF) {
					t.Fatalf("device activation occurred or peer failed early: %v", peerErr)
				}
			case <-time.After(time.Second):
				t.Fatal("synthetic EIS peer did not finish after setup cancellation")
			}
			if sender != nil {
				_ = sender.client.close()
				t.Fatalf("EIS setup returned a live sender after caller cancellation: %v", err)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("EIS setup error = %v, want caller cancellation", err)
			}
		})
	}
}

// Frame constructors must not encode events for unbound capabilities or
// panic when a revoked/absent sender is passed into a direct helper.
func TestEISFrameConstructorsRejectUnboundCapabilitiesWithoutPanicking(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"nil pointer sender", func() error { _, err := eiPointerFrame(nil, 10, 10); return err }},
		{"nil text sender", func() error { _, err := eiTextFrames(nil, "private text"); return err }},
		{"nil scroll sender", func() error { _, err := eiScrollFrame(nil, 0, 120); return err }},
		{"scroll no client", func() error { _, err := eiScrollFrame(&eiSender{scroll: 11, scrollDev: 12}, 0, 120); return err }},
		{"scroll no interface", func() error {
			_, err := eiScrollFrame(&eiSender{client: &eiClient{}, scrollDev: 12}, 0, 120)
			return err
		}},
		{"scroll no device", func() error { _, err := eiScrollFrame(&eiSender{client: &eiClient{}, scroll: 11}, 0, 120); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("unbound frame constructor panicked: %v", p)
				}
			}()
			if err := tc.run(); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unbound frame returned %v rather than unsupported", err)
			}
		})
	}
}

// A disconnected or unbound pointer does not own its advertised region.
// A valid authorized pointer must remain selectable even when stale EIS
// device metadata advertises an overlapping region.
func TestEISUnboundPointerCannotMaskLiveOverlappingPointer(t *testing.T) {
	for _, tc := range []struct {
		name string
		dead eiPointerDevice
	}{
		{"unbound device", eiPointerDevice{device: 0, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}},
		{"unbound pointer", eiPointerDevice{device: 501, pointer: 0, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := eiPointerDevice{device: 201, pointer: 202, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}
			for _, pointers := range [][]eiPointerDevice{{tc.dead, live}, {live, tc.dead}} {
				sender := &eiSender{pointers: pointers}
				pointer, ok := sender.pointerForPoint(25, 30)
				if !ok || pointer.device != live.device || pointer.pointer != live.pointer {
					t.Fatalf("stale overlapping pointer masked live authorization: got=%+v ok=%v", pointer, ok)
				}
			}
		})
	}
}

func TestEISPointerRegionWithoutBoundDeviceNeverAuthorizesCoordinates(t *testing.T) {
	for _, pointer := range []eiPointerDevice{
		{device: 0, pointer: 202, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}},
		{device: 201, pointer: 0, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}},
	} {
		sender := &eiSender{pointers: []eiPointerDevice{pointer}}
		if sender.validatePoint(25, 30) {
			t.Fatalf("unbound pointer accepted coordinate authorization %+v", pointer)
		}
		if sender.hasAuthorizedPointer() {
			t.Fatalf("unbound pointer advertised capability %+v", pointer)
		}
		if _, err := eiPointerFrame(sender, 25, 30); err == nil || errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("unbound pointer frame result %v", err)
		}
	}
}

func TestRemoteDesktopMouseClickCancellationRevokesHeldButton(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- backend.MouseClick(ctx, 300, 400, 1) }()

	// Observe the authorized move and button press before canceling, so the
	// test proves teardown after the button has become an external side effect.
	assertSyntheticEISOperation(t, server, eiServerIDFloor+101, 1)
	assertSyntheticEISOperation(t, server, eiServerIDFloor+102, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled click error = %v, want caller cancellation", err)
	}
	<-backend.writerDone
	if err := backend.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("click cleanup left the authorized session usable: %v", err)
	}
}

// A compositor may not authorize two unrelated input interfaces using the
// same protocol object ID. Otherwise messages for one interface may be sent
// with opcodes belonging to another.
func TestEISRejectsAliasedInterfaceObjectAcrossCapabilities(t *testing.T) {
	const deviceID = eiServerIDFloor + 20
	sender := &eiSender{devices: map[uint64]*eiDevice{deviceID: {id: deviceID, version: 1, interfaces: map[string]uint64{}}}}
	device := sender.devices[deviceID]
	first := eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...)}
	if err := sender.consumeSetupEvent(first); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ei_button", "ei_scroll", "ei_pointer_absolute", "ei_text"} {
		err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString(name)...)})
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("aliased interface %q accepted: %v", name, err)
		}
		if len(device.interfaces) != 1 || device.interfaces["ei_keyboard"] != eiServerIDFloor+101 {
			t.Errorf("alias changed existing authorization: %+v", device.interfaces)
		}
	}
	if err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+102), eiString("ei_button")...)}); err != nil {
		t.Fatalf("distinct legitimate interface rejected: %v", err)
	}
}

// The peer's ping must be answered on the negotiated connection socket, not
// merely acknowledged as an incoming event. The reply is an actual EI frame.
func TestEISClientRepliesToAuthorizedPingOnRealUnixSocket(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const connection = eiServerIDFloor + 11
	const ping = eiServerIDFloor + 12
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if got := client.connection.Load(); got != connection {
		t.Fatalf("negotiated connection=%x", got)
	}
	frame := eiRequest(connection, 3, append(eiU64(ping), eiU32(1)...))
	if _, err := serverConn.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 24)
	total := 0
	for total < len(reply) {
		n, err := serverConn.Read(reply[total:])
		if err != nil {
			t.Fatalf("missing ping response after %d bytes: %v", total, err)
		}
		total += n
	}
	if got := binary.NativeEndian.Uint64(reply[0:8]); got != ping {
		t.Fatalf("pong target=%x want=%x", got, ping)
	}
	if got := binary.NativeEndian.Uint32(reply[8:12]); got != 24 {
		t.Fatalf("pong frame size=%d", got)
	}
	if got := binary.NativeEndian.Uint32(reply[12:16]); got != 0 {
		t.Fatalf("pong opcode=%d", got)
	}
	if got := binary.NativeEndian.Uint64(reply[16:24]); got != 0 {
		t.Fatalf("pong payload=%d", got)
	}
	event, err := client.next(ctx)
	if err != nil || event.object != connection || event.opcode != 3 {
		t.Fatalf("ping event lost or altered %+v %v", event, err)
	}
}

func TestEISClientRejectsZeroPingObjectBeforeResponding(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const connection = eiServerIDFloor + 11
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(eiRequest(connection, 3, append(eiU64(0), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
			t.Fatalf("zero ping not rejected: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("invalid ping did not close client")
	}
}

// A peer's malformed ancillary framing cannot authorize a file descriptor
// release based on an incomplete or forged control-message header.
func TestEISMalformedAncillaryHeaderIsDiscarded(t *testing.T) {
	header := make([]byte, unix.CmsgSpace(4))
	// Claim a payload larger than the actual received control buffer.
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.NativeEndian.PutUint64(header[:8], uint64(len(header)+256))
	} else {
		binary.NativeEndian.PutUint32(header[:4], uint32(len(header)+256))
	}
	closeReceivedFDs(header)
}

// A syntactically valid SCM_RIGHTS header with a partial descriptor payload
// must not be decoded as an integer descriptor or closed.
func TestEISPartialRightsPayloadIsIgnored(t *testing.T) {
	control := unix.UnixRights(0)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.NativeEndian.PutUint64(control[:8], uint64(unix.CmsgLen(1)))
	} else {
		binary.NativeEndian.PutUint32(control[:4], uint32(unix.CmsgLen(1)))
	}
	closeReceivedFDs(control[:unix.CmsgLen(1)])
}

// The EIS reader must close unsolicited descriptors even when the carrying
// frame is malformed and its transport is about to terminate. Otherwise a
// sender can retain live pipe/socket resources in the receiving process.
func TestEISMalformedWireFramesReleaseUnsolicitedDescriptors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
		want  string
	}{
		{name: "invalid frame length", frame: func() []byte {
			v := eiRequest(eiServerIDFloor+7, 1, nil)
			binary.NativeEndian.PutUint32(v[8:12], 12)
			return v
		}(), want: "invalid message size"},
		{name: "invalid handshake connection", frame: eiRequest(0, 2, eiU64(3)), want: "invalid handshake connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(incoming)
			defer client.close()
			var pipe [2]int
			if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
				t.Fatal(err)
			}
			defer unix.Close(pipe[0])
			if _, _, err := peer.WriteMsgUnix(tc.frame, unix.UnixRights(pipe[1]), nil); err != nil {
				_ = unix.Close(pipe[1])
				t.Fatal(err)
			}
			if err := unix.Close(pipe[1]); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
			case <-time.After(time.Second):
				t.Fatal("malformed frame did not terminate EIS reader")
			}
			if err := client.readError(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected protocol failure: %v, want %s", err, tc.want)
			}
			var byteBuf [1]byte
			n, err := unix.Read(pipe[0], byteBuf[:])
			if n != 0 || err != nil {
				t.Fatalf("reader leaked transferred pipe write end: n=%d err=%v", n, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			event, nextErr := client.next(ctx)
			if nextErr == nil || event.object != 0 {
				t.Fatalf("malformed transport delivered event: %+v %v", event, nextErr)
			}
			if errors.Is(nextErr, context.DeadlineExceeded) {
				t.Fatalf("terminal EIS stream remained active: %v", nextErr)
			}
		})
	}
}

// An untrusted EIS peer may send multiple ancillary records. Only SCM_RIGHTS
// descriptors belong to the receiver; unrelated control records must neither
// prevent their closure nor cause a different live descriptor to be closed.
func TestEISMixedAncillaryControlClosesOnlyTransferredDescriptors(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	first, err := unix.Dup(pipe[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := unix.Dup(pipe[1])
	if err != nil {
		unix.Close(first)
		t.Fatal(err)
	}
	control := append(unix.UnixCredentials(&unix.Ucred{Pid: 123, Uid: 234, Gid: 345}), unix.UnixRights(first, second)...)
	closeReceivedFDs(control)
	for _, fd := range []int{first, second} {
		if _, checkErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(checkErr, unix.EBADF) {
			_ = unix.Close(fd)
			t.Fatalf("transferred descriptor %d remained open: %v", fd, checkErr)
		}
	}
	for _, fd := range pipe {
		if _, checkErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); checkErr != nil {
			t.Fatalf("original descriptor %d was closed: %v", fd, checkErr)
		}
	}
}

func TestEISSetupSelectsCapabilitiesAcrossResumedDevices(t *testing.T) {
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	const (
		connectionID = eiServerIDFloor + 0x10
		seatID       = eiServerIDFloor + 0x11
		pointerID    = eiServerIDFloor + 0x20
		keyboardID   = eiServerIDFloor + 0x21
	)
	stream := eiRequest(0, 0, eiU32(1))
	connection := append(eiU32(4), eiU64(connectionID)...)
	connection = append(connection, eiU32(1)...)
	stream = append(stream, eiRequest(0, 2, connection)...)
	stream = append(stream, eiRequest(connectionID, 1, eiU64(seatID))...)
	for i, capability := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		body := append(eiU64(uint64(1<<i)), eiString(capability)...)
		stream = append(stream, eiRequest(seatID, 2, body)...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	stream = append(stream, eiRequest(seatID, 4, append(eiU64(pointerID), eiU32(1)...))...)
	stream = append(stream, eiRequest(pointerID, 5, append(eiU64(eiServerIDFloor+0x30), eiString("ei_pointer_absolute")...))...)
	region := append(eiU32(10), eiU32(20)...)
	region = append(region, eiU32(200)...)
	region = append(region, eiU32(120)...)
	region = append(region, eiF32(1)...)
	stream = append(stream, eiRequest(pointerID, 4, region)...)
	stream = append(stream, eiRequest(pointerID, 6, nil)...)
	stream = append(stream, eiRequest(pointerID, 7, eiU32(5))...)
	stream = append(stream, eiRequest(seatID, 4, append(eiU64(keyboardID), eiU32(1)...))...)
	stream = append(stream, eiRequest(keyboardID, 5, append(eiU64(eiServerIDFloor+0x31), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(keyboardID, 5, append(eiU64(eiServerIDFloor+0x32), eiString("ei_button")...))...)
	stream = append(stream, eiRequest(keyboardID, 6, nil)...)
	stream = append(stream, eiRequest(keyboardID, 7, eiU32(6))...)
	stream = append(stream, eiRequest(1, 0, eiU64(0))...)
	for len(stream) > 0 {
		n, err := server.Write(stream)
		if err != nil {
			t.Fatalf("write synthetic multi-device setup: %v", err)
		}
		stream = stream[n:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if err != nil {
		t.Fatalf("setup EIS sender from split capabilities: %v", err)
	}
	t.Cleanup(func() { _ = sender.client.close() })
	if sender.keyboardDev != keyboardID || sender.buttonDev != keyboardID || sender.pointerDev != pointerID {
		t.Fatalf("capabilities selected across resumed devices incorrectly: keyboard=%x button=%x pointer=%x", sender.keyboardDev, sender.buttonDev, sender.pointerDev)
	}
	if sender.scroll != 0 || sender.text != 0 || len(sender.pointers) != 1 || !sender.validatePoint(20, 30) {
		t.Fatalf("optional capabilities or pointer region are incorrect: scroll=%x text=%x pointers=%+v", sender.scroll, sender.text, sender.pointers)
	}
	for i := 0; i < 14; i++ {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatalf("read sender negotiation request %d: %v", i, err)
		}
		if request.object != 0 {
			t.Fatalf("negotiation request %d used unexpected object %x", i, request.object)
		}
		if i == 13 && request.opcode != 1 {
			t.Fatalf("last negotiation request opcode = %d, want handshake finish", request.opcode)
		}
	}
	if request, err := readSyntheticEISFrame(server); err != nil || request.object != seatID || request.opcode != 1 {
		t.Fatalf("seat capabilities were not bound: %+v %v", request, err)
	}
	if request, err := readSyntheticEISFrame(server); err != nil || request.object != connectionID || request.opcode != 0 {
		t.Fatalf("device setup synchronization was not requested: %+v %v", request, err)
	}
	for _, want := range []uint64{keyboardID, pointerID} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatalf("read selected device start: %v", err)
		}
		if request.object != want || request.opcode != 1 || len(request.body) != 8 || binary.NativeEndian.Uint32(request.body[4:8]) != 1 {
			t.Fatalf("unexpected selected-device start = %+v, want device %x sequence 1", request, want)
		}
	}
}

// A partial EIS message never authorizes a protocol event. If the peer closes
// before completing the frame, pending reads must terminate rather than
// waiting for a frame that no live peer can finish.
func TestEISPartialFramePeerCloseNeverPublishesEvent(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	client := newEIClient(clientConn)
	defer client.close()
	frame := eiRequest(eiServerIDFloor+7, 5, eiU32(9))
	if _, err := serverConn.Write(frame[:len(frame)-4]); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-client.messages:
		t.Fatalf("partial frame published event: %+v", message)
	case <-time.After(10 * time.Millisecond):
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader survived peer close with incomplete frame")
	}
	if err := client.readError(); !errors.Is(err, io.EOF) {
		t.Fatalf("partial frame terminal error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if message, err := client.next(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) || message.object != 0 {
		t.Fatalf("terminal partial frame yielded event=%+v err=%v", message, err)
	}
}

// Once the parser observes a malformed message, even a preceding valid event
// already buffered by the reader cannot be treated as current authorization.
func TestEISMalformedFrameRevokesQueuedEarlierEvent(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	valid := eiRequest(eiServerIDFloor+8, 5, eiU32(11))
	invalid := eiRequest(eiServerIDFloor+9, 5, eiU32(12))
	binary.NativeEndian.PutUint32(invalid[8:12], 12)
	if _, err := serverConn.Write(append(valid, invalid...)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader did not retire malformed frame")
	}
	if err := client.readError(); err == nil || !strings.Contains(err.Error(), "invalid message size") {
		t.Fatalf("parser terminal error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := client.next(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || message.object != 0 || client.serial.Load() != 0 {
		t.Fatalf("queued event escaped failed transport: message=%+v err=%v serial=%d", message, err, client.serial.Load())
	}
}

func TestEISenderWithoutAuthorizedPointerRejectsNilAndEmptyDevices(t *testing.T) {
	var nilSender *eiSender
	if nilSender.hasAuthorizedPointer() {
		t.Fatal("nil sender advertised pointer authorization")
	}
	for _, sender := range []*eiSender{
		{},
		{pointers: []eiPointerDevice{{device: 3, pointer: 4, regions: []eiRegion{{width: 0, height: 1}}}}},
		{pointers: []eiPointerDevice{{device: 3, regions: []eiRegion{{width: 1, height: 1}}}}},
	} {
		if sender.hasAuthorizedPointer() {
			t.Fatalf("incomplete EIS pointer advertised capability: %+v", sender)
		}
	}
}

// After EI has learned the authoritative connection ID, a valid server
// ping must not be acknowledged if the local reply stream has been retired.
// The reply failure must be returned and the connection identity preserved.
func TestEIServerPingFailsClosedWhenReplyTransportIsUnavailable(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer clientConn.Close()
	defer serverConn.Close()
	const connection = eiServerIDFloor + 31
	const ping = eiServerIDFloor + 32
	client := &eiClient{conn: clientConn}
	first := eiMessage{object: 0, opcode: 2, body: append(append(eiU32(17), eiU64(connection)...), eiU32(1)...)}
	if err := client.handleProtocolMessage(first); err != nil {
		t.Fatalf("valid connection event rejected: %v", err)
	}
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response := eiMessage{object: connection, opcode: 3, body: append(eiU64(ping), eiU32(1)...)}
	err := client.handleProtocolMessage(response)
	if !errors.Is(err, unix.EPIPE) {
		t.Fatalf("unavailable reply transport acknowledged ping: %v", err)
	}
	if got := client.connection.Load(); got != connection {
		t.Fatalf("closed reply transport changed connection authority: %d", got)
	}
}

// The connection object and pingpong object must have distinct identities.
// Sending ei_pingpong.done to the connection object would be dispatched as an
// unrelated connection request, not a valid ping response.
func TestEISRejectsPingObjectAliasedToConnection(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	const connection = eiServerIDFloor + 21
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := peer.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(connection), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
			t.Fatalf("aliased ping was accepted: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("aliased ping did not terminate the stream")
	}
}

// Once the server's connection object is authenticated, every malformed
// ping must close the stream without making an acknowledgment on that socket.
// The peer must not be able to keep using stale input authority afterwards.
func TestEISLiveConnectionRejectsMalformedPingWithoutAcknowledging(t *testing.T) {
	const ping = eiServerIDFloor + 22
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"missing version", eiU64(ping), "invalid connection ping size"},
		{"short ping object", eiU32(1), "invalid connection ping size"},
		{"extended ping", append(append(eiU64(ping), eiU32(1)...), eiU32(0)...), "invalid connection ping size"},
		{"zero object", append(eiU64(0), eiU32(1)...), "unsupported connection ping"},
		{"zero version", append(eiU64(ping), eiU32(0)...), "unsupported connection ping"},
		{"future version", append(eiU64(ping), eiU32(2)...), "unsupported connection ping"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkEISInvalidPing(t, tc.body, tc.want) })
	}
}

// checkEISInvalidPing exercises an independently negotiated real socket.
func checkEISInvalidPing(t *testing.T, body []byte, want string) {
	const connection = eiServerIDFloor + 21
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if client.connection.Load() != connection {
		t.Fatal("handshake failed to establish connection")
	}
	if _, err := serverConn.Write(eiRequest(connection, 3, body)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("malformed ping error=%v, want %q", err, want)
		}
	case <-ctx.Done():
		t.Fatal("malformed ping left EIS stream active")
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 64)
	count, err := serverConn.Read(data)
	if count != 0 || err == nil {
		t.Fatalf("malformed ping received successful reply: n=%d, err=%v", count, err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected no ping response before deadline: %v", err)
	}
}

// A ping is acknowledged only after a successful transport write. When the
// server disappears, the authenticated peer must not be told that it received
// an EIS pong, and the underlying socket error must remain observable.
func TestEISAuthorizedPingReportsBrokenReplyTransport(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	client := &eiClient{conn: clientConn}
	t.Cleanup(func() { _ = clientConn.Close() })
	const connection = eiServerIDFloor + 21
	const ping = eiServerIDFloor + 22
	client.connection.Store(connection)
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	err := client.handleProtocolMessage(eiMessage{object: connection, opcode: 3, body: append(eiU64(ping), eiU32(1)...)})
	if err == nil || !strings.Contains(err.Error(), "answer connection ping") {
		t.Fatalf("broken reply transport accepted ping: %v", err)
	}
	var netErr *net.OpError
	if !errors.As(err, &netErr) {
		t.Fatalf("lost socket transport failure: %T %v", err, err)
	}
}

func TestEIReadLoopClosesUnexpectedReceivedDescriptors(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	t.Cleanup(func() { _ = serverConn.Close() })
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(pipe[0]) })
	t.Cleanup(func() { _ = unix.Close(pipe[1]) })

	client := newEIClient(clientConn)
	t.Cleanup(func() { _ = client.close() })
	message := eiRequest(eiServerIDFloor+1, 1, eiU32(1))
	if _, controlBytes, err := serverConn.WriteMsgUnix(message, unix.UnixRights(pipe[1]), nil); err != nil {
		t.Fatalf("send EIS message with unexpected descriptor: %v", err)
	} else if controlBytes == 0 {
		t.Fatal("descriptor control message was not sent")
	}
	got, err := client.next(t.Context())
	if err != nil {
		t.Fatalf("receive EIS event: %v", err)
	}
	if got.object != eiServerIDFloor+1 || got.opcode != 1 {
		t.Fatalf("received wrong EIS event: %+v", got)
	}
	if err := unix.Close(pipe[1]); err != nil {
		t.Fatal(err)
	}
	pipe[1] = -1
	var data [1]byte
	n, err := unix.Read(pipe[0], data[:])
	if err != nil || n != 0 {
		t.Fatalf("receiver retained an unsolicited descriptor: read = (%d, %v), want EOF", n, err)
	}
}

func TestEISAbsoluteRegionBoundaryAndDeviceConflict(t *testing.T) {
	s := &eiSender{pointers: []eiPointerDevice{
		{device: 11, pointer: 111, regions: []eiRegion{{x: 10, y: 20, width: 100, height: 50, scale: 1}}},
		{device: 22, pointer: 222, regions: []eiRegion{{x: 110, y: 20, width: 100, height: 50, scale: 1}}},
	}}
	for _, tc := range []struct {
		x, y   int
		device uint64
		valid  bool
	}{
		{10, 20, 11, true}, {109, 69, 11, true}, {110, 20, 22, true}, {209, 69, 22, true},
		{9, 20, 0, false}, {10, 19, 0, false}, {109, 70, 0, false}, {210, 69, 0, false},
		{-1, 20, 0, false}, {10, -1, 0, false}, {math.MaxInt, 20, 0, false},
	} {
		p, ok := s.pointerForPoint(tc.x, tc.y)
		if ok != tc.valid || ok && p.device != tc.device {
			t.Errorf("point (%d,%d) got device %d valid %v, wanted device %d valid %v", tc.x, tc.y, p.device, ok, tc.device, tc.valid)
		}
	}
	s.pointers = append(s.pointers, eiPointerDevice{device: 33, pointer: 333, regions: []eiRegion{{x: 90, y: 30, width: 40, height: 20, scale: 1}}})
	if s.validatePoint(100, 40) {
		t.Fatal("overlap on competing devices was accepted")
	}
	if !s.validatePoint(10, 20) {
		t.Fatal("nonoverlapping region was wrongly rejected")
	}
}

func TestEISPointerRegionRandomBoundaryProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(99812))
	for i := 0; i < 3000; i++ {
		x, y := rng.Intn(1000), rng.Intn(1000)
		w, h := rng.Intn(500)+1, rng.Intn(500)+1
		s := &eiSender{pointers: []eiPointerDevice{{device: 1, pointer: 5, regions: []eiRegion{{x: uint32(x), y: uint32(y), width: uint32(w), height: uint32(h), scale: 1}}}}}
		for _, px := range []int{x - 1, x, x + w/2, x + w - 1, x + w} {
			for _, py := range []int{y - 1, y, y + h/2, y + h - 1, y + h} {
				want := px >= x && px < x+w && py >= y && py < y+h
				if got := s.validatePoint(px, py); got != want {
					t.Fatalf("%d: region (%d,%d) %dx%d point (%d,%d) got %t want %t", i, x, y, w, h, px, py, got, want)
				}
			}
		}
	}
}

func TestEISPointerFrameRejectsInexactFloat32Coordinates(t *testing.T) {
	s := &eiSender{client: &eiClient{}, pointers: []eiPointerDevice{{device: 1, pointer: 5, regions: []eiRegion{{x: 0, y: 0, width: 1 << 26, height: 500, scale: 1}}}}}
	for _, tc := range []struct {
		x       int
		wantErr bool
	}{{0, false}, {1 << 24, false}, {1<<24 + 1, true}, {(1 << 24) + 2, false}} {
		_, err := eiPointerFrame(s, tc.x, 100)
		if (err != nil) != tc.wantErr {
			t.Errorf("x=%d err=%v wantErr=%t", tc.x, err, tc.wantErr)
		}
	}
}

// The compositor must not grant an absolute region whose far edge wraps
// outside the uint32 coordinate domain used to describe its origin and size.
func TestEISSetupRejectsUnrepresentableAbsoluteRegionBounds(t *testing.T) {
	cases := []struct {
		name                string
		x, y, width, height uint32
	}{
		{"x endpoint overflow", math.MaxUint32, 0, 2, 1},
		{"y endpoint overflow", 0, math.MaxUint32, 1, 2},
		{"both endpoints overflow", math.MaxUint32, math.MaxUint32, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
			body := append(eiU32(tc.x), eiU32(tc.y)...)
			body = append(body, eiU32(tc.width)...)
			body = append(body, eiU32(tc.height)...)
			body = append(body, eiF32(1)...)
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: body}); err == nil {
				t.Fatalf("accepted absolute region with unrepresentable boundary: %+v", tc)
			}
			if got := len(sender.devices[42].regions); got != 0 {
				t.Fatalf("invalid region acquired authorization: %d", got)
			}
		})
	}
}

// An exclusive far edge exactly at 2^32 is still representable as the
// boundary after the final uint32 coordinate and must not be mistaken for
// an overflow into a different coordinate space.
func TestEISSetupAllowsLastRepresentableCoordinate(t *testing.T) {
	sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
	body := append(eiU32(math.MaxUint32), eiU32(math.MaxUint32)...)
	body = append(body, eiU32(1)...)
	body = append(body, eiU32(1)...)
	body = append(body, eiF32(1)...)
	if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: body}); err != nil {
		t.Fatalf("rejected final representable position: %v", err)
	}
	if got := len(sender.devices[42].regions); got != 1 {
		t.Fatalf("expected one boundary region, got %d", got)
	}
}

// Every field boundary in the compositor's region advertisement is untrusted.
// A partial frame must be rejected before it can change a device's permitted
// absolute coordinate region or the device's other negotiated capabilities.
func TestEISRegionFieldTruncationNeverWidensDeviceAuthority(t *testing.T) {
	const deviceID = eiServerIDFloor + 0x151
	baseline := eiRegion{x: 4, y: 6, width: 40, height: 30, scale: 1}
	full := append(append(append(append(eiU32(11), eiU32(13)...), eiU32(100)...), eiU32(80)...), eiF32(1.25)...)
	for _, tc := range []struct {
		name   string
		length int
	}{
		{"missing x", 0}, {"missing y", 4}, {"missing width", 8}, {"missing height", 12}, {"missing scale", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := &eiDevice{id: deviceID, version: 1, interfaces: map[string]uint64{"ei_keyboard": deviceID + 1}, regions: []eiRegion{baseline}}
			s := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := s.consumeSetupEvent(eiMessage{object: deviceID, opcode: 4, body: full[:tc.length]})
			if err == nil {
				t.Fatal("accepted incomplete absolute region")
			}
			if len(device.regions) != 1 || device.regions[0] != baseline || device.interfaces["ei_keyboard"] != deviceID+1 {
				t.Fatalf("malformed region changed negotiated permission: regions=%+v interfaces=%+v", device.regions, device.interfaces)
			}
		})
	}
}

// An interface announcement can be truncated before either its object ID or
// name is complete. Incomplete metadata must never grant a new input object.
func TestEISInterfaceFieldTruncationPreservesPriorAuthorization(t *testing.T) {
	const deviceID = eiServerIDFloor + 0x161
	full := append(eiU64(deviceID+12), eiString("ei_pointer_absolute")...)
	for _, tc := range []struct {
		name   string
		length int
	}{
		{"missing object", 0}, {"partial object", 4}, {"missing name length", 8}, {"partial name payload", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := map[string]uint64{"ei_keyboard": deviceID + 10}
			device := &eiDevice{id: deviceID, version: 1, interfaces: existing}
			s := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := s.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: full[:tc.length]})
			if err == nil || strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("incomplete interface was not rejected as invalid: %v", err)
			}
			if len(device.interfaces) != 1 || device.interfaces["ei_keyboard"] != deviceID+10 {
				t.Fatalf("incomplete interface changed authority: %+v", device.interfaces)
			}
		})
	}
}

// Scroll values are signed protocol distances. A sign or byte-order reversal
// would move the desktop in the opposite direction even when EIS accepts it.
func TestEISScrollFramePreservesSignedDistancesAndDeviceSerial(t *testing.T) {
	sender := &eiSender{client: &eiClient{}, scroll: 0x102, scrollDev: 0x203}
	sender.client.serial.Store(0x11223344)
	for _, tc := range []struct {
		name string
		x, y int32
	}{
		{"vertical up", 0, -120},
		{"vertical down", 0, 120},
		{"horizontal left", -360, 0},
		{"horizontal right", 360, 0},
		{"signed extremes", math.MinInt32, math.MaxInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := eiScrollFrame(sender, tc.x, tc.y)
			if err != nil {
				t.Fatal(err)
			}
			request, rest := splitEIRequest(t, encoded)
			if request.object != sender.scroll || request.opcode != 2 || len(request.body) != 8 {
				t.Fatalf("scroll request = %+v", request)
			}
			x := int32(binary.NativeEndian.Uint32(request.body[:4]))
			y := int32(binary.NativeEndian.Uint32(request.body[4:]))
			if x != tc.x || y != tc.y {
				t.Fatalf("encoded distances (%d,%d), want (%d,%d)", x, y, tc.x, tc.y)
			}
			frame, trailing := splitEIRequest(t, rest)
			if frame.object != sender.scrollDev || frame.opcode != 3 || len(frame.body) != 12 || len(trailing) != 0 {
				t.Fatalf("device frame = %+v, trailing %d bytes", frame, len(trailing))
			}
			if serial := binary.NativeEndian.Uint32(frame.body[:4]); serial != 0x11223344 {
				t.Fatalf("frame serial = %#x", serial)
			}
			if stamp := binary.NativeEndian.Uint64(frame.body[4:]); stamp == 0 {
				t.Fatal("frame missing monotonic timestamp")
			}
		})
	}
}

// ei_seat.done carries no payload. A server cannot smuggle additional data
// into the capability-completion event and still authorize device binding.
func TestEISRejectsExtendedSeatCapabilityCompletionOverWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"extra word", eiU32(0)},
		{"extra uint64", eiU64(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, cancel, done := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const conn = eiServerIDFloor + 0x10
			const seat = eiServerIDFloor + 0x11
			stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(conn)...), eiU32(1)...))
			stream = append(stream, eiRequest(conn, 1, eiU64(seat))...)
			for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
				stream = append(stream, eiRequest(seat, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
			}
			stream = append(stream, eiRequest(seat, 3, tc.body)...)
			if _, err := server.Write(stream); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat capability completion") {
					t.Fatalf("extended seat done accepted: sender=%v err=%v", result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("extended seat.done advanced setup instead of failing closed")
			}
		})
	}
}

// EIS protocol objects have connection-wide identities; the seat cannot be
// the connection itself, or authorization and capability messages are aliased.
func TestEISHandshakeRejectsSeatAliasedToConnection(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	events = append(events, eiRequest(connection, 1, eiU64(connection))...)
	if _, err := server.Write(events); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
			t.Fatalf("aliased seat accepted: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("aliased EIS seat not rejected promptly")
	}
}

// A seat removed before capability enumeration completes is no longer an
// authorized target. Setup must not wait for a completion message from a
// destroyed seat, or publish a sender based on its stale grant.
func TestEISSeatRemovalDuringCapabilityEnumerationRevokesImmediately(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	const seat = eiServerIDFloor + 0x11
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
	stream = append(stream, eiRequest(seat, 2, append(eiU64(1), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(seat, 0, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("removed seat accepted or misclassified: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("removed seat left EIS setup waiting for stale capabilities")
	}
}

// A seat can be destroyed after capability binding but before any input
// devices are ready. The setup handshake must revoke on either side of its
// callback barrier instead of waiting for now-impossible device events.
func TestEISSeatRemovalDuringDeviceSetupRevokesImmediately(t *testing.T) {
	for _, tc := range []struct {
		name           string
		finishCallback bool
	}{
		{"before setup callback", false},
		{"after setup callback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkEISSeatRemovalDuringDeviceSetup(t, tc.finishCallback)
		})
	}
}

func checkEISSeatRemovalDuringDeviceSetup(t *testing.T, finishCallback bool) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	const seat = eiServerIDFloor + 0x11
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		stream = append(stream, eiRequest(seat, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seat, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	callback := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 || callback.object != connection || callback.opcode != 0 {
		t.Fatalf("unexpected setup requests: %+v %+v", bind, callback)
	}
	if finishCallback {
		completeEISSeatRemovalSetupCallback(t, server, callback)
	}
	if _, err := server.Write(eiRequest(seat, 0, nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("removed seat retained authority: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("seat removal left setup waiting for device enumeration")
	}
}

func completeEISSeatRemovalSetupCallback(t *testing.T, server *net.UnixConn, callback testEIRequest) {
	t.Helper()
	callbackID, err := eiReadU64(callback.body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(eiRequest(callbackID, 0, eiU64(0))); err != nil {
		t.Fatal(err)
	}
}

// EI assigns object IDs from distinct client and server namespaces. Accepting
// a server connection object with a client-owned ID aliases setup callbacks
// and subsequent connection messages with unrelated client requests.
func TestEISRejectsServerConnectionInClientObjectNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{name: "first callback ID", id: 1},
		{name: "subsequent client object", id: 17},
		{name: "highest client-owned ID", id: eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(conn)
			defer client.close()
			handshake := eiRequest(0, 2, append(append(eiU32(9), eiU64(tc.id)...), eiU32(1)...))
			if _, err := peer.Write(handshake); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "connection") {
					t.Fatalf("invalid connection ID %x not rejected: %v", tc.id, err)
				}
				if got := client.connection.Load(); got != 0 {
					t.Fatalf("invalid connection ID became authoritative: %x", got)
				}
				if msg, err := client.next(ctx); err == nil || msg.object != 0 {
					t.Fatalf("invalid ID delivered to protocol setup: %+v %v", msg, err)
				}
			case <-ctx.Done():
				t.Fatalf("invalid connection ID %x remained accepted", tc.id)
			}
		})
	}
}

func TestEISAcceptsConnectionAtServerObjectNamespaceBoundary(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	handshake := eiRequest(0, 2, append(append(eiU32(9), eiU64(eiServerIDFloor)...), eiU32(1)...))
	if _, err := peer.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if msg, err := client.next(ctx); err != nil || msg.object != 0 || msg.opcode != 2 {
		t.Fatalf("valid boundary connection failed: %+v %v", msg, err)
	}
	if got := client.connection.Load(); got != eiServerIDFloor {
		t.Fatalf("boundary connection stored as %x", got)
	}
}

// A server-announced ei_device must use the server object namespace; it may
// otherwise alias client callbacks used during setup and future requests.
func TestEISDeviceAnnouncementRejectsClientObjectNamespace(t *testing.T) {
	const seat = eiServerIDFloor + 11
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"callback object", 1},
		{"later client object", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &eiSender{seat: seat, connection: eiServerIDFloor + 10, devices: make(map[uint64]*eiDevice)}
			err := s.consumeSetupEvent(eiMessage{object: seat, opcode: 4, body: append(eiU64(tc.id), eiU32(1)...)})
			if err == nil || !strings.Contains(err.Error(), "invalid device") {
				t.Fatalf("client-owned object %x accepted as device: %v", tc.id, err)
			}
			if len(s.devices) != 0 {
				t.Fatalf("rejected device retained authority: %v", s.devices)
			}
		})
	}
}

func TestEISDeviceAnnouncementAcceptsServerNamespaceBoundary(t *testing.T) {
	s := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: make(map[uint64]*eiDevice)}
	err := s.consumeSetupEvent(eiMessage{object: s.seat, opcode: 4, body: append(eiU64(eiServerIDFloor), eiU32(1)...)})
	if err != nil || s.devices[eiServerIDFloor] == nil {
		t.Fatalf("server device namespace boundary rejected: %v", err)
	}
}

// The server owns the interface objects it announces on an ei_device. An
// interface from the client namespace could alias the device-setup callback.
func TestEISRejectsInterfaceInClientObjectNamespace(t *testing.T) {
	const dev = eiServerIDFloor + 32
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"callback", 1},
		{"subsequent client object", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: map[uint64]*eiDevice{dev: {id: dev, version: 1, interfaces: make(map[string]uint64)}}}
			err := sender.consumeSetupEvent(eiMessage{object: dev, opcode: 5, body: append(eiU64(tc.id), eiString("ei_keyboard")...)})
			if err == nil || !strings.Contains(err.Error(), "invalid interface") {
				t.Fatalf("client-owned interface %x accepted: %v", tc.id, err)
			}
			if len(sender.devices[dev].interfaces) != 0 {
				t.Fatalf("invalid interface retained: %v", sender.devices[dev].interfaces)
			}
		})
	}
}

func TestEISAcceptsInterfaceAtServerNamespaceBoundary(t *testing.T) {
	const dev = eiServerIDFloor + 32
	sender := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: map[uint64]*eiDevice{dev: {id: dev, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: dev, opcode: 5, body: append(eiU64(eiServerIDFloor), eiString("ei_keyboard")...)})
	if err != nil || sender.devices[dev].interfaces["ei_keyboard"] != eiServerIDFloor {
		t.Fatalf("server-owned interface rejected: %v %v", err, sender.devices[dev].interfaces)
	}
}

// A server-created pingpong object cannot borrow a client-owned object ID.
// A reply on such an ID would send a request to a different protocol object.
func TestEISRejectsServerPingInClientObjectNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"first callback", 1},
		{"later callback", 17},
		{"namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(conn)
			defer client.close()
			const connection = eiServerIDFloor + 21
			if _, err := peer.Write(eiRequest(0, 2, append(append(eiU32(3), eiU64(connection)...), eiU32(1)...))); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.next(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(tc.id), eiU32(1)...))); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
					t.Fatalf("client-owned ping %x accepted: %v", tc.id, err)
				}
			case <-ctx.Done():
				t.Fatalf("client-owned ping %x left transport active", tc.id)
			}
		})
	}
}

func TestEISAcceptsServerPingAtNamespaceBoundary(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	const connection = eiServerIDFloor + 21
	if _, err := peer.Write(eiRequest(0, 2, append(append(eiU32(3), eiU64(connection)...), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(eiServerIDFloor), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 24)
	total := 0
	for total < len(reply) {
		n, err := peer.Read(reply[total:])
		if err != nil {
			t.Fatalf("valid server-owned ping not answered: %v", err)
		}
		total += n
	}
	expected := eiRequest(eiServerIDFloor, 0, eiU64(0))
	if string(reply) != string(expected) {
		t.Fatalf("invalid server-owned pong %x", reply)
	}
}

// Server-advertised seats are protocol objects. A seat in the client-owned
// range can collide with the callback object allocated during device setup.
func TestEISHandshakeRejectsServerSeatInClientNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		seat uint64
	}{
		{"first client callback", 1},
		{"later client callback", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, cancel, done := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const connection = eiServerIDFloor + 0x10
			events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
			events = append(events, eiRequest(connection, 1, eiU64(tc.seat))...)
			if _, err := server.Write(events); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
					t.Fatalf("client-owned seat %x accepted: sender=%v err=%v", tc.seat, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("client-owned seat %x not rejected before capabilities", tc.seat)
			}
		})
	}
}

// The ei_callback.done event carries exactly one uint64. A peer may not
// complete setup with trailing data that changes the callback frame shape.
func TestEISSetupCallbackRejectsExtraPayloadOnWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"extra word", append(eiU64(0), eiU32(0)...)},
		{"extra uint64", append(eiU64(0), eiU64(0)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			// prepareEISInvalidGrantEndpoint reads the synchronization request,
			// whose first client-created callback is object 1.
			if _, err := server.Write(eiRequest(1, 0, tc.body)); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device setup callback") {
					t.Fatalf("malformed synchronization callback accepted: sender=%v err=%v", result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("extra callback payload left EIS setup awaiting devices")
			}
		})
	}
}

// The client-owned ei_callback has only a done event. A peer sending another
// opcode to that callback must be rejected rather than silently ignored while
// input setup waits for a completion event that may never come.
func TestEISSetupCallbackRejectsUnexpectedOpcodeOnWire(t *testing.T) {
	for _, opcode := range []uint32{1, 2, 3} {
		t.Run(string(rune('0'+opcode)), func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			if _, err := server.Write(eiRequest(1, opcode, eiU64(0))); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device setup callback") {
					t.Fatalf("unexpected callback opcode %d accepted: sender=%v err=%v", opcode, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("callback opcode %d ignored without rejecting setup", opcode)
			}
		})
	}
}

func TestEIClientReassemblesFragmentedRealUnixFramesInOrder(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	messages := []eiMessage{
		{object: eiServerIDFloor + 1, opcode: 5, body: eiU32(1)},
		{object: eiServerIDFloor + 2, opcode: 7, body: eiU32(2)},
		{object: eiServerIDFloor + 3, opcode: 8, body: eiU32(3)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i, original := range messages {
		frame := eiRequest(original.object, original.opcode, original.body)
		// Deliberately split across header, size, opcode and payload boundaries.
		for len(frame) > 0 {
			n := min(1+i, len(frame))
			if _, err := serverConn.Write(frame[:n]); err != nil {
				t.Fatal(err)
			}
			frame = frame[n:]
		}
	}
	for _, want := range messages {
		actual, err := client.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if actual.object != want.object || actual.opcode != want.opcode || !bytes.Equal(actual.body, want.body) {
			t.Fatalf("misordered or corrupted EIS packet got=%+v want=%+v", actual, want)
		}
	}
	if got := client.serial.Load(); got != 3 {
		t.Fatalf("serial after input state event %d, want 3", got)
	}
}

func TestEIClientRejectsPeerClosingMidProtocolFrame(t *testing.T) {
	for _, cut := range []int{1, 7, 11, 15, 16, 17, 20} {
		t.Run(strings.ReplaceAll(string(rune('a'+cut)), "/", "_"), func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			client := newEIClient(conn)
			defer client.close()
			frame := eiRequest(eiServerIDFloor+3, 6, append(eiU64(1), eiU64(2)...))
			if cut >= len(frame) {
				t.Fatal("invalid test cut")
			}
			if _, err := peer.Write(frame[:cut]); err != nil {
				t.Fatal(err)
			}
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if client.readError() == nil {
					t.Fatal("truncated EIS stream appeared successful")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if event, err := client.next(ctx); err == nil || event.object != 0 {
					t.Fatalf("partial EIS message leaked as valid event=%+v error=%v", event, err)
				}
			case <-time.After(time.Second):
				t.Fatal("partial EIS stream not closed")
			}
		})
	}
}

func TestEIClientCloseUnblocksReaderWhenInboundEventQueueIsFull(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	frame := eiRequest(eiServerIDFloor+1, 5, eiU32(1))
	payload := bytes.Repeat(frame, 180)
	if _, err := peer.Write(payload); err != nil {
		t.Fatal(err)
	}
	// Wait until the bounded queue fills, then terminate the reader while it
	// is blocked on enqueue rather than consuming any of those events.
	for len(client.messages) < cap(client.messages) {
		select {
		case <-ctx.Done():
			t.Fatal("EIS reader never filled bounded queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := client.close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-ctx.Done():
		t.Fatal("EIS reader did not exit after close with full queue")
	}
	if err := client.readError(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("unrecognized read termination: %v", err)
	}
}

// Socket disconnection must be terminal even if protocol messages were already
// buffered. Replayed EIS events from a dead authorization session are not
// authoritative grants for ongoing device negotiation.
func TestEIClientRefusesBufferedEventsAfterTransportTerminalFailure(t *testing.T) {
	failure := errors.New("synthetic EIS peer disconnected")
	for i := 0; i < 128; i++ {
		c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
		c.messages <- eiMessage{object: 0, opcode: 2, body: append(append(eiU32(1), eiU64(0xff00000000000010)...), eiU32(1)...)}
		c.setReadError(failure)
		close(c.done)
		event, err := c.next(context.Background())
		if !errors.Is(err, failure) {
			t.Fatalf("iteration %d accepted stale EIS event %+v instead of terminal failure: %v", i, event, err)
		}
	}
}

func TestEISenderRejectsPeerClosedBeforeHandshake(t *testing.T) {
	client, peer := unixConnectionPair(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if sender != nil || err == nil || !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "handshake event") {
		t.Fatalf("closed EIS peer returned sender=%v err=%v, want handshake EOF", sender, err)
	}
}

func TestEISenderReportsPeerWriteShutdownDuringHandshake(t *testing.T) {
	client, peer := unixConnectionPair(t)
	defer peer.Close()
	if _, err := peer.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	if err := peer.CloseRead(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if sender != nil || err == nil || !strings.Contains(err.Error(), "configure sender") || !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("closed EIS command path returned sender=%v err=%v", sender, err)
	}
}

func TestEISenderReportsPeerWriteShutdownWhileBindingSeat(t *testing.T) {
	server, ctx, cancel, result := startObservedEISenderSetup(t)
	defer cancel()
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send handshake version: %v", err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)

	const connectionID = uint64(0xff00000000000010)
	const pingID = uint64(0xff00000000000011)
	assertEISenderAnswersPing(t, server, connectionID, pingID)
	awaitEISetupWait(t, ctx, 3)

	const seatID = uint64(0xff00000000000020)
	if _, err := server.Write(eiRequest(connectionID, 1, eiU64(seatID))); err != nil {
		t.Fatalf("send seat identity: %v", err)
	}
	awaitEISetupWait(t, ctx, 4)
	for i, capability := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		body := append(eiU64(1<<i), eiString(capability)...)
		if _, err := server.Write(eiRequest(seatID, 2, body)); err != nil {
			t.Fatalf("send %s seat capability: %v", capability, err)
		}
		awaitEISetupWait(t, ctx, uint32(5+i))
	}
	if err := server.CloseRead(); err != nil {
		t.Fatalf("shut down peer read side: %v", err)
	}
	if _, err := server.Write(eiRequest(seatID, 3, nil)); err != nil {
		t.Fatalf("finish seat capabilities: %v", err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "bind seat") || !errors.Is(err, syscall.EPIPE) {
			t.Fatalf("seat bind write failure = %v, want bind seat and EPIPE", err)
		}
	case <-ctx.Done():
		t.Fatalf("EIS setup did not report seat bind write failure: %v", ctx.Err())
	}
}

func TestEITextFramesSplitAndTerminateEachTextRequest(t *testing.T) {
	client := &eiClient{}
	sender := &eiSender{client: client, text: 41, textDev: 42}
	value := strings.Repeat("a", 253) + "€" + "z"
	frames, err := eiTextFrames(sender, value)
	if err != nil {
		t.Fatalf("eiTextFrames: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("text frames = %d, want 2", len(frames))
	}
	chunks := make([]string, 0, len(frames))
	for _, frame := range frames {
		request, rest := splitEIRequest(t, frame)
		if request.object != sender.text || request.opcode != 2 {
			t.Fatalf("text request = object %d opcode %d, want object %d opcode 2", request.object, request.opcode, sender.text)
		}
		chunk, err := eiReadString(request.body, 0)
		if err != nil {
			t.Fatalf("decode text request: %v", err)
		}
		if len(chunk) > 254 || !utf8.ValidString(chunk) {
			t.Fatalf("text chunk has invalid size or UTF-8: %d bytes %q", len(chunk), chunk)
		}
		deviceFrame, trailing := splitEIRequest(t, rest)
		if deviceFrame.object != sender.textDev || deviceFrame.opcode != 3 || len(deviceFrame.body) != 12 || len(trailing) != 0 {
			t.Fatalf("text device frame = %+v, trailing bytes %d", deviceFrame, len(trailing))
		}
		chunks = append(chunks, chunk)
	}
	if strings.Join(chunks, "") != value {
		t.Fatalf("text chunks join to %q, want %q", strings.Join(chunks, ""), value)
	}
}

func TestEIPointerFrameRoutesByUniqueDeviceRegion(t *testing.T) {
	sender := &eiSender{
		client: &eiClient{},
		pointers: []eiPointerDevice{
			{device: 20, pointer: 120, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}},
			{device: 30, pointer: 130, regions: []eiRegion{{x: 100, y: 0, width: 100, height: 100, scale: 1}}},
		},
	}
	frame, err := eiPointerFrame(sender, 125, 50)
	if err != nil {
		t.Fatalf("eiPointerFrame: %v", err)
	}
	request, rest := splitEIRequest(t, frame)
	if request.object != 130 || request.opcode != 1 {
		t.Fatalf("pointer request = object %d opcode %d, want object 130 opcode 1", request.object, request.opcode)
	}
	x := math.Float32frombits(binary.NativeEndian.Uint32(request.body[:4]))
	y := math.Float32frombits(binary.NativeEndian.Uint32(request.body[4:8]))
	if x != 125 || y != 50 {
		t.Fatalf("pointer coordinates = %g,%g, want 125,50", x, y)
	}
	deviceFrame, trailing := splitEIRequest(t, rest)
	if deviceFrame.object != 30 || deviceFrame.opcode != 3 || len(trailing) != 0 {
		t.Fatalf("pointer device frame = %+v, trailing bytes %d", deviceFrame, len(trailing))
	}
	if !sender.validatePoint(25, 50) || !sender.validatePoint(125, 50) || sender.validatePoint(250, 50) {
		t.Fatal("region validation did not accept only advertised logical coordinates")
	}
	sender.pointers = append(sender.pointers, eiPointerDevice{
		device: 40, pointer: 140, regions: []eiRegion{{x: 120, y: 0, width: 100, height: 100, scale: 1}},
	})
	if sender.validatePoint(125, 50) {
		t.Fatal("overlapping regions from different devices must not select an arbitrary pointer")
	}
}

func TestEISKeyTapUsesOneFramePerStateTransition(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	client := newEIClient(clientConn)
	defer client.close()
	defer serverConn.Close()
	sender := &eiSender{client: client, keyboard: 51, keyboardDev: 52}
	backend := &RemoteDesktopBackend{sender: sender, heldKeys: make(map[uint32]bool)}
	if err := backend.typeAction(keySend{key: "a", modifiers: modifiers{shift: true}}, 30); err != nil {
		t.Fatalf("typeAction: %v", err)
	}
	want := []struct {
		key   uint32
		state uint32
	}{{42, 1}, {30, 1}, {30, 0}, {42, 0}}
	for index, expected := range want {
		request := readEIRequest(t, serverConn)
		if request.object != sender.keyboard || request.opcode != 1 || len(request.body) != 8 {
			t.Fatalf("keyboard request %d = %+v", index, request)
		}
		key := binary.NativeEndian.Uint32(request.body[:4])
		state := binary.NativeEndian.Uint32(request.body[4:8])
		if key != expected.key || state != expected.state {
			t.Fatalf("keyboard transition %d = key %d state %d, want key %d state %d", index, key, state, expected.key, expected.state)
		}
		frame := readEIRequest(t, serverConn)
		if frame.object != sender.keyboardDev || frame.opcode != 3 {
			t.Fatalf("keyboard transition %d frame = %+v", index, frame)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("held keys after tap = %v, want none", backend.heldKeys)
	}
}

func TestEISenderNegotiatesAndAnswersConnectionPing(t *testing.T) {
	serverConn, ctx, cancel, setupDone := startEISenderSetup(t)
	defer cancel()
	if _, err := serverConn.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send EIS handshake version: %v", err)
	}
	assertEISenderAnnouncesPingPong(t, serverConn)
	const connectionID = uint64(0xff00000000000010)
	const pingID = uint64(0xff00000000000011)
	assertEISenderAnswersPing(t, serverConn, connectionID, pingID)
	if _, err := serverConn.Write(eiRequest(connectionID, 0, eiU32(7))); err != nil {
		t.Fatalf("send EIS disconnect event: %v", err)
	}
	select {
	case err := <-setupDone:
		if !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("setup error = %v, want %v", err, ErrRemoteDesktopRevoked)
		}
	case <-ctx.Done():
		t.Fatalf("EIS setup did not observe disconnect before deadline: %v", ctx.Err())
	}
}

func startEISenderSetup(t *testing.T) (*net.UnixConn, context.Context, context.CancelFunc, <-chan error) {
	t.Helper()
	clientConn, serverConn := unixConnectionPair(t)
	t.Cleanup(func() { _ = serverConn.Close() })
	if err := serverConn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set EIS server deadline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	setupDone := make(chan error, 1)
	go func() {
		_, err := setupEISender(ctx, clientConn)
		setupDone <- err
	}()
	return serverConn, ctx, cancel, setupDone
}

func assertEISenderAnnouncesPingPong(t *testing.T, serverConn *net.UnixConn) {
	t.Helper()
	for _, opcode := range []uint32{0, 2, 3} {
		request := readEIRequest(t, serverConn)
		if request.object != 0 || request.opcode != opcode {
			t.Fatalf("handshake request = object %d opcode %d, want object 0 opcode %d", request.object, request.opcode, opcode)
		}
	}
	announcedPingPong := false
	for {
		request := readEIRequest(t, serverConn)
		if request.object != 0 {
			t.Fatalf("interface announcement object = %d, want handshake object 0", request.object)
		}
		switch request.opcode {
		case 1:
			if !announcedPingPong {
				t.Fatal("EIS handshake did not announce ei_pingpong support")
			}
			return
		case 4:
			name, err := eiReadString(request.body, 0)
			if err != nil {
				t.Fatalf("decode announced interface: %v", err)
			}
			announcedPingPong = announcedPingPong || name == "ei_pingpong"
		default:
			t.Fatalf("handshake request opcode = %d, want interface announcement or finish", request.opcode)
		}
	}
}

func assertEISenderAnswersPing(t *testing.T, serverConn *net.UnixConn, connectionID, pingID uint64) {
	t.Helper()
	connectionEvent := eiRequest(0, 2, append(append(eiU32(7), eiU64(connectionID)...), eiU32(1)...))
	pingEvent := eiRequest(connectionID, 3, append(eiU64(pingID), eiU32(1)...))
	if _, err := serverConn.Write(append(connectionEvent, pingEvent...)); err != nil {
		t.Fatalf("send connection and ping events: %v", err)
	}
	pong := readEIRequest(t, serverConn)
	if pong.object != pingID || pong.opcode != 0 || len(pong.body) != 8 || binary.NativeEndian.Uint64(pong.body) != 0 {
		t.Fatalf("ping response = %+v, want ei_pingpong.done(0) on object %d", pong, pingID)
	}
}

func TestRemoteDesktopBackendStopsAfterEISLifecycleLoss(t *testing.T) {
	backend := &RemoteDesktopBackend{sender: &eiSender{
		connection: 10, seat: 11, keyboard: 12, keyboardDev: 13,
		pointers: []eiPointerDevice{{device: 14, pointer: 15}},
	}}
	for _, message := range []eiMessage{
		{object: 10, opcode: 0},
		{object: 11, opcode: 0},
		{object: 12, opcode: 0},
		{object: 13, opcode: 8},
		{object: 14, opcode: 8},
		{object: 15, opcode: 0},
	} {
		if !backend.isEISLifecycleFailure(message) {
			t.Errorf("lifecycle message %+v did not stop the selected EIS session", message)
		}
	}
	if backend.isEISLifecycleFailure(eiMessage{object: 99, opcode: 8}) {
		t.Fatal("unselected device pause stopped the EIS session")
	}
}

func TestRemoteDesktopBackendCancellationInterruptsBlockedWrite(t *testing.T) { //nolint:contextcheck // the test exercises cancellation while session revocation owns independent teardown.
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	rawConn, err := clientConn.SyscallConn()
	if err != nil {
		t.Fatalf("get EIS socket descriptor: %v", err)
	}
	var socketOptionErr error
	if err := rawConn.Control(func(fd uintptr) {
		socketOptionErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, 4096)
	}); err != nil {
		t.Fatalf("access EIS socket descriptor: %v", err)
	}
	if socketOptionErr != nil {
		t.Fatalf("set small EIS send buffer: %v", socketOptionErr)
	}
	client := newEIClient(clientConn)
	portal := &portalSession{}
	backend := newRemoteDesktopBackend(&eiSender{client: client}, portal)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	writeDone := make(chan error, 1)
	result := make(chan error, 1)
	go func() {
		result <- backend.submit(ctx, func() error { //nolint:contextcheck // writer revocation owns independent portal teardown.
			close(started)
			err := backend.write(make([]byte, 1<<20))
			writeDone <- err
			return err
		})
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("canceled write error = %v, want cancellation or revoked transport", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt the blocked EIS write")
	}
	select {
	case writeErr := <-writeDone:
		if writeErr == nil {
			t.Fatal("canceled blocked EIS write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled writer did not finish after its socket deadline was interrupted")
	}
	if err := backend.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("backend state after uncertain canceled write = %v, want revoked", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

type testEIRequest struct {
	object uint64
	opcode uint32
	body   []byte
}

func splitEIRequest(t *testing.T, data []byte) (testEIRequest, []byte) {
	t.Helper()
	if len(data) < eiHeaderSize {
		t.Fatalf("EIS data has %d bytes, want header", len(data))
	}
	size := int(binary.NativeEndian.Uint32(data[8:12]))
	if size < eiHeaderSize || size > len(data) || size%4 != 0 {
		t.Fatalf("invalid EIS message size %d for %d bytes", size, len(data))
	}
	return testEIRequest{
		object: binary.NativeEndian.Uint64(data[:8]),
		opcode: binary.NativeEndian.Uint32(data[12:16]),
		body:   append([]byte(nil), data[16:size]...),
	}, data[size:]
}

func readEIRequest(t *testing.T, reader io.Reader) testEIRequest {
	t.Helper()
	header := make([]byte, eiHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("read EIS header: %v", err)
	}
	size := int(binary.NativeEndian.Uint32(header[8:12]))
	if size < eiHeaderSize || size > eiMaximumMessage || size%4 != 0 {
		t.Fatalf("invalid EIS message size %d", size)
	}
	message := append([]byte(nil), header...)
	message = append(message, make([]byte, size-eiHeaderSize)...)
	if _, err := io.ReadFull(reader, message[eiHeaderSize:]); err != nil {
		t.Fatalf("read EIS body: %v", err)
	}
	parsed, rest := splitEIRequest(t, message)
	if len(rest) != 0 {
		t.Fatalf("read EIS parser left %d bytes", len(rest))
	}
	return parsed
}

func unixConnectionPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	path := testutil.ShortSocketPath(t, "eis.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on local Unix socket: %v", err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("dial local Unix socket: %v", err)
	}
	server, err := listener.AcceptUnix()
	if err != nil {
		_ = client.Close()
		t.Fatalf("accept local Unix socket: %v", err)
	}
	return client, server
}

// EIS strings are NUL-terminated and cannot contain a second terminator
// inside their body. Reject the entire input before emitting any text frame.
func TestEISTextFramesRejectEmbeddedNULWithoutPartialOutput(t *testing.T) {
	sender := &eiSender{client: &eiClient{}, text: 202, textDev: 201}
	for _, text := range []string{"\x00", "prefix\x00suffix", strings.Repeat("a", 260) + "\x00tail"} {
		frames, err := eiTextFrames(sender, text)
		if err == nil || len(frames) != 0 {
			t.Errorf("accepted embedded NUL in %q: frames=%d err=%v", text, len(frames), err)
		}
	}
	frames, err := eiTextFrames(sender, "valid text")
	if err != nil || len(frames) != 1 {
		t.Fatalf("valid text rejected: frames=%d err=%v", len(frames), err)
	}
}

func TestEISHandshakeRejectsTruncatedSeatIdentity(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)

	const connection = eiServerIDFloor + 0x22
	events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	events = append(events, eiRequest(connection, 1, eiU32(9))...)
	if _, err := server.Write(events); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "truncated uint64 argument") {
			t.Fatalf("truncated seat identity result = sender %v, err %v", result.sender, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("EIS setup did not reject the truncated seat identity")
	}
}

// A multi-frame text transaction is valid only if each encoded EIS text
// argument contains complete UTF-8 code points, each frame targets the
// negotiated text device and every frame retains the negotiated serial.
func TestEISUnicodeTextMultipleFramesKeepCompleteRunesAndSerial(t *testing.T) {
	client := &eiClient{}
	client.serial.Store(0x7fa11234)
	sender := &eiSender{client: client, text: eiServerIDFloor + 31, textDev: eiServerIDFloor + 32}
	for _, tc := range []struct {
		name, content string
		minFrames     int
	}{
		{"four-byte boundary", strings.Repeat("a", 253) + "🛶" + strings.Repeat("€", 95), 2},
		{"many multibyte frames", strings.Repeat("🛶éç漢", 200), 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := eiTextFrames(sender, tc.content)
			if err != nil || len(frames) < tc.minFrames {
				t.Fatalf("text frame count=%d err=%v", len(frames), err)
			}
			var restored strings.Builder
			for i, wire := range frames {
				request, remaining := splitEIRequest(t, wire)
				if request.object != sender.text || request.opcode != 2 {
					t.Fatalf("frame %d text event=%+v", i, request)
				}
				chunk, err := eiReadString(request.body, 0)
				if err != nil || len(chunk) == 0 || len(chunk) > 254 || !utf8.ValidString(chunk) {
					t.Fatalf("frame %d chunk bytes=%d err=%v", i, len(chunk), err)
				}
				restored.WriteString(chunk)
				device, trailing := splitEIRequest(t, remaining)
				if len(trailing) != 0 || device.object != sender.textDev || device.opcode != 3 || len(device.body) != 12 {
					t.Fatalf("frame %d device=%+v trailing=%d", i, device, len(trailing))
				}
				if serial := binary.NativeEndian.Uint32(device.body[:4]); serial != 0x7fa11234 {
					t.Fatalf("frame %d serial=%08x", i, serial)
				}
			}
			if restored.String() != tc.content {
				t.Fatal("multi-frame text changed codepoints or ordering")
			}
		})
	}
}

// EIS global events are delivered on one stream. A forged or stale capability
// event from an unrelated seat must not mutate the negotiated seat's device
// mask or prematurely finish its capability enumeration.
func TestEISSetupIgnoresOtherSeatCapabilityAndCompletionEvents(t *testing.T) {
	server, ctx, cancel, result := startObservedEISenderSetup(t)
	defer cancel()
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)
	const connection = eiServerIDFloor + 16
	const ping = eiServerIDFloor + 17
	assertEISenderAnswersPing(t, server, connection, ping)
	awaitEISetupWait(t, ctx, 3)
	const seat = eiServerIDFloor + 32
	const untrustedSeat = eiServerIDFloor + 33
	if _, err := server.Write(eiRequest(connection, 1, eiU64(seat))); err != nil {
		t.Fatal(err)
	}
	awaitEISetupWait(t, ctx, 4)
	fake := append(eiRequest(untrustedSeat, 2, append(eiU64(0xffffffff), eiString("ei_keyboard")...)), eiRequest(untrustedSeat, 3, nil)...)
	if _, err := server.Write(fake); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		if _, err := server.Write(eiRequest(seat, 2, append(eiU64(1<<i), eiString(name)...))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.Write(eiRequest(seat, 3, nil)); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 || len(bind.body) != 8 || binary.NativeEndian.Uint64(bind.body) != 7 {
		t.Fatalf("foreign seat influenced bound capability mask: %+v", bind)
	}
	sync := readEIRequest(t, server)
	if sync.object != connection || sync.opcode != 0 {
		t.Fatalf("foreign seat caused invalid synchronization: %+v", sync)
	}
	awaitEISetupWait(t, ctx, 5)
	cancel()
	assertEISSetupCanceled(t, result)
}
