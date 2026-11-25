//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Multiple physical pointer devices can expose disjoint absolute regions.
// Their advertisement order must not change which authoritative interface
// receives a coordinate, and a gap must not be sent to either device.
func TestEISDeviceReadinessSelectsMultipleDisjointPointersDeterministically(t *testing.T) {
	const low = eiServerIDFloor + 0x80
	const high = eiServerIDFloor + 0x90
	for _, order := range [][]uint64{{high, low}, {low, high}} {
		devices := map[uint64]*eiDevice{}
		for _, id := range order {
			x := uint32(0)
			if id == high {
				x = 100
			}
			devices[id] = &eiDevice{id: id, done: true, resumed: true, interfaces: map[string]uint64{"ei_pointer_absolute": id + 1}, regions: []eiRegion{{x: x, y: 0, width: 80, height: 60, scale: 1}}}
		}
		// Full capability readiness requires keyboard and buttons as well.
		devices[low].interfaces["ei_keyboard"] = low + 2
		devices[high].interfaces["ei_button"] = high + 2
		sender := &eiSender{client: &eiClient{}, devices: devices}
		if !sender.devicesReady() {
			t.Fatalf("valid split devices were not ready: %+v", sender)
		}
		if len(sender.pointers) != 2 || sender.pointers[0].device != low || sender.pointers[1].device != high || sender.pointerDev != low || sender.keyboardDev != low || sender.buttonDev != high {
			t.Fatalf("device selection depended on announcement order %v: %+v", order, sender)
		}
		for _, tc := range []struct {
			x, y   int
			device uint64
			ok     bool
		}{{1, 1, low, true}, {79, 59, low, true}, {100, 0, high, true}, {179, 59, high, true}, {80, 1, 0, false}, {180, 1, 0, false}} {
			chosen, ok := sender.pointerForPoint(tc.x, tc.y)
			if ok != tc.ok || (ok && chosen.device != tc.device) {
				t.Fatalf("%v: point %d,%d routed to %x ok=%t", order, tc.x, tc.y, chosen.device, ok)
			}
			frame, err := eiPointerFrame(sender, tc.x, tc.y)
			if !tc.ok {
				if err == nil || frame != nil {
					t.Fatalf("%v: unowned point %d,%d got frame %x, %v", order, tc.x, tc.y, frame, err)
				}
				continue
			}
			if err != nil || len(frame) < 14 {
				t.Fatalf("%v: owned point %d,%d rejected: %v", order, tc.x, tc.y, err)
			}
			if object := binary.NativeEndian.Uint64(frame[:8]); object != tc.device+1 {
				t.Fatalf("%v: point %d,%d sent to %x, expected pointer interface %x", order, tc.x, tc.y, object, tc.device+1)
			}
		}
	}
}

// A peer can close only its sending direction without closing the Unix socket.
// This still terminates the EI event stream and must retire protocol authority;
// the reader must not wait forever for another event or deliver a zero frame.
func TestEIClientPeerWriteHalfCloseTerminatesReaderAndRejectsEvents(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	if err := serverConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("EI reader remained active after peer half-close")
	}
	if err := client.readError(); err == nil || (!errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "peer closed")) {
		t.Fatalf("peer half-close was not terminal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := client.next(ctx)
	if err == nil || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("half-closed transport delivered an event: %+v %v", event, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, net.ErrClosed) {
		t.Fatalf("EI reader did not publish the actual half-close failure: %v", err)
	}
}

func TestEISenderRejectsMalformedConnectionAnnouncementSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "empty body", body: nil},
		{name: "serial without connection identity", body: eiU32(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, result := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatalf("send handshake version: %v", err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			if _, err := server.Write(eiRequest(0, 2, tc.body)); err != nil {
				t.Fatalf("send malformed connection announcement: %v", err)
			}
			select {
			case setup := <-result:
				wantSize := "size 0"
				if len(tc.body) != 0 {
					wantSize = "size 4"
				}
				if setup.sender != nil || setup.err == nil || !strings.Contains(setup.err.Error(), "invalid handshake connection event "+wantSize) {
					t.Fatalf("malformed connection announcement returned sender=%v err=%v", setup.sender, setup.err)
				}
			case <-ctx.Done():
				t.Fatalf("setup did not reject malformed connection announcement: %v", ctx.Err())
			}
		})
	}
}

type eisDeviceResumeWait struct {
	server     *net.UnixConn
	ctx        *eiObservedWaitContext
	result     <-chan error
	connection uint64
	seat       uint64
	callback   uint64
}

func prepareEISenderAtDeviceResumeWait(t *testing.T) eisDeviceResumeWait {
	t.Helper()
	server, ctx, _, result := startObservedEISenderSetup(t)
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send handshake version: %v", err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)
	const connection = uint64(0xff00000000000010)
	const seat = uint64(0xff00000000000020)
	assertEISenderAnswersPing(t, server, connection, eiServerIDFloor+0x31)
	awaitEISetupWait(t, ctx, 3)
	if _, err := server.Write(eiRequest(connection, 1, eiU64(seat))); err != nil {
		t.Fatalf("send seat identity: %v", err)
	}
	awaitEISetupWait(t, ctx, 4)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		capability := append(eiU64(uint64(1<<i)), eiString(name)...)
		if _, err := server.Write(eiRequest(seat, 2, capability)); err != nil {
			t.Fatalf("send %s seat capability: %v", name, err)
		}
	}
	if _, err := server.Write(eiRequest(seat, 3, nil)); err != nil {
		t.Fatalf("finish seat capabilities: %v", err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 {
		t.Fatalf("seat binding request = %+v", bind)
	}
	synchronize := readEIRequest(t, server)
	if synchronize.object != connection || synchronize.opcode != 0 || len(synchronize.body) != 12 {
		t.Fatalf("device synchronization request = %+v", synchronize)
	}
	callback := binary.NativeEndian.Uint64(synchronize.body[:8])
	awaitEISetupWait(t, ctx, 5)
	return eisDeviceResumeWait{
		server: server, ctx: ctx, result: result,
		connection: connection, seat: seat, callback: callback,
	}
}

func TestEISenderRejectsMalformedEventWhileAwaitingResumedDevices(t *testing.T) {
	wait := prepareEISenderAtDeviceResumeWait(t)
	callbackDone := eiRequest(wait.callback, 0, eiU64(0))
	if _, err := wait.server.Write(callbackDone); err != nil {
		t.Fatalf("complete device synchronization: %v", err)
	}
	awaitEISetupWait(t, wait.ctx, 6)
	malformedDevice := eiRequest(wait.seat, 4, eiU64(eiServerIDFloor+0x41))
	if _, err := wait.server.Write(malformedDevice); err != nil {
		t.Fatalf("send malformed device event: %v", err)
	}
	select {
	case err := <-wait.result:
		if err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("malformed resumed device event error = %v", err)
		}
	case <-wait.ctx.Done():
		t.Fatalf("setup did not reject malformed resumed device event: %v", wait.ctx.Err())
	}
}

func TestRemoteDesktopRejectsPointerCoordinateNotRepresentableByEIS(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 1<<24 + 4, height: 100, scale: 1}},
	}}

	err := backend.MouseMove(context.Background(), 1<<24+1, 10)
	if err == nil || !strings.Contains(err.Error(), "cannot be represented exactly") {
		t.Fatalf("inexact EIS coordinate error = %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if backend.hasPointer {
		t.Fatal("unrepresentable pointer coordinate changed recorded location")
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("coordinate validation error retired the session: %v", err)
	}
}

// The first six checks bracket the handshake, connection, and seat messages.
const eisSeatCapabilityWaitErrCall = 7

// blockEISContextErr pauses setup at a known client.next boundary.
type blockEISContextErr struct {
	context.Context
	blockAt int
	calls   int
	reached chan struct{}
	release chan struct{}
}

func (c *blockEISContextErr) Err() error {
	c.calls++
	if c.calls == c.blockAt {
		close(c.reached)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}

func TestSetupEISenderFailsWhenSeatCapabilityStreamEnds(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	baseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx := &blockEISContextErr{
		Context: baseCtx,
		blockAt: eisSeatCapabilityWaitErrCall,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		_, err := setupEISender(ctx, clientConn)
		result <- err
	}()
	if _, err := peer.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send EIS handshake version: %v", err)
	}
	for index := 0; index < 14; index++ {
		request, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("read sender setup request %d: %v", index+1, err)
		}
		if request.object != 0 {
			t.Fatalf("setup request %d targets object %d, want display object 0", index+1, request.object)
		}
		wantOpcode := uint32(4)
		switch index {
		case 0:
			wantOpcode = 0
		case 1:
			wantOpcode = 2
		case 2:
			wantOpcode = 3
		case 13:
			wantOpcode = 1
		}
		if request.opcode != wantOpcode {
			t.Fatalf("setup request %d opcode %d is out of handshake order", index+1, request.opcode)
		}
	}
	const connectionID = eiServerIDFloor + 0x10
	const seatID = eiServerIDFloor + 0x11
	connection := append(eiU32(7), eiU64(connectionID)...)
	connection = append(connection, eiU32(1)...)
	stream := append(eiRequest(0, 2, connection), eiRequest(connectionID, 1, eiU64(seatID))...)
	if _, err := peer.Write(stream); err != nil {
		t.Fatalf("send EIS connection and seat events: %v", err)
	}
	select {
	case <-ctx.reached:
	case <-baseCtx.Done():
		t.Fatalf("setup did not begin reading seat capabilities: %v", baseCtx.Err())
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	close(ctx.release)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "read seat capabilities") {
			t.Fatalf("seat capability stream termination returned %v", err)
		}
	case <-baseCtx.Done():
		t.Fatalf("setup did not observe the closed seat capability stream: %v", baseCtx.Err())
	}
}

// Only protocol events that carry a serial may advance the outgoing EIS
// sequence. In particular, a disconnect body contains a reason, not a serial.
func TestEISSerialAuthorityProtocolEventMatrix(t *testing.T) {
	const connection = eiServerIDFloor + 16
	cases := []struct {
		name  string
		event eiMessage
		want  uint32
	}{
		{"handshake connection", eiMessage{object: 0, opcode: 2, body: eiU32(101)}, 101},
		{"new device", eiMessage{object: connection + 1, opcode: 0, body: eiU32(102)}, 102},
		{"device resumed", eiMessage{object: connection + 2, opcode: 7, body: eiU32(103)}, 103},
		{"device paused", eiMessage{object: connection + 3, opcode: 8, body: eiU32(104)}, 104},
		{"disconnect reason not serial", eiMessage{object: connection, opcode: 0, body: eiU32(888)}, 77},
		{"short device serial", eiMessage{object: connection + 1, opcode: 0, body: []byte{8, 9, 10}}, 77},
		{"unrelated client object", eiMessage{object: 4, opcode: 7, body: eiU32(888)}, 77},
		{"unrelated global opcode", eiMessage{object: 0, opcode: 7, body: eiU32(888)}, 77},
		{"unrelated server opcode", eiMessage{object: connection + 1, opcode: 10, body: eiU32(888)}, 77},
		{"empty packet", eiMessage{}, 77},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &eiClient{}
			c.connection.Store(connection)
			c.serial.Store(77)
			c.updateSerial(tc.event)
			if actual := c.serial.Load(); actual != tc.want {
				t.Fatalf("serial=%d want=%d for event %+v", actual, tc.want, tc.event)
			}
		})
	}
}
