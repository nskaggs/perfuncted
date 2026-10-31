package input

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEIReadLoopRejectsInvalidFrameSizes(t *testing.T) {
	for _, size := range []uint32{0, 8, 17, 18, eiMaximumMessage + 4} {
		t.Run(strings.ReplaceAll(string(rune(size)), "/", "_"), func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			header := make([]byte, eiHeaderSize)
			binary.NativeEndian.PutUint32(header[8:12], size)
			if _, err := serverConn.Write(header); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "invalid message size") {
					t.Fatalf("frame size %d: got %v, want invalid size error", size, err)
				}
			case <-time.After(time.Second):
				t.Fatalf("parser did not reject frame size %d", size)
			}
		})
	}
}

func TestEIReadLoopRejectsMalformedConnectionAndPing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages [][]byte
		want     string
	}{
		{"short connection", [][]byte{eiRequest(0, 2, eiU64(12))}, "invalid handshake connection"},
		{"zero connection", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(0)...), eiU32(1)...))}, "unsupported handshake"},
		{"wrong version", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(2)...))}, "unsupported handshake"},
		{"short ping", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(1)...)), eiRequest(11, 3, eiU64(3))}, "invalid connection ping"},
		{"wrong ping version", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(1)...)), eiRequest(11, 3, append(eiU64(3), eiU32(2)...))}, "unsupported connection ping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			for _, message := range tc.messages {
				if _, err := serverConn.Write(message); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("read error = %v, want %q", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("malformed protocol was not rejected")
			}
		})
	}
}

func TestEISenderRejectsIncompleteNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		initial        []byte
		afterHandshake []byte
		want           string
		revoked        bool
	}{
		{"missing version", eiRequest(0, 0, nil), nil, "missing handshake version", false},
		{"version zero", eiRequest(0, 0, eiU32(0)), nil, "version zero", false},
		{"missing capabilities", eiRequest(0, 0, eiU32(1)), nil, "does not offer", false},
		{"disconnect before seat", eiRequest(0, 0, eiU32(1)), nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, done := startEISenderSetup(t)
			defer cancel()
			if _, err := server.Write(tc.initial); err != nil {
				t.Fatal(err)
			}
			if tc.name != "missing version" && tc.name != "version zero" {
				assertEISenderAnnouncesPingPong(t, server)
				const connection = uint64(0xff00000000000010)
				if _, err := server.Write(eiRequest(0, 2, append(append(eiU32(1), eiU64(connection)...), eiU32(1)...))); err != nil {
					t.Fatal(err)
				}
				if tc.revoked {
					if _, err := server.Write(eiRequest(connection, 0, eiU32(1))); err != nil {
						t.Fatal(err)
					}
				} else {
					const seat = uint64(0xff00000000000020)
					if _, err := server.Write(append(eiRequest(connection, 1, eiU64(seat)), eiRequest(seat, 3, nil)...)); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case err := <-done:
				if tc.revoked {
					if !errors.Is(err, ErrRemoteDesktopRevoked) {
						t.Fatalf("got %v, want revoked", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want %q", err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("negotiation did not reject invalid peer: %v", ctx.Err())
			}
		})
	}
}

func TestEISetupEventRejectsInvalidRegions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint32
		scale         float32
		truncate      bool
	}{
		{"zero width", 0, 50, 1, false},
		{"zero height", 50, 0, 1, false},
		{"zero scale", 50, 50, 0, false},
		{"nan scale", 50, 50, float32(math.NaN()), false},
		{"infinite scale", 50, 50, float32(math.Inf(1)), false},
		{"truncated region", 50, 50, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
			data := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(tc.width)...), eiU32(tc.height)...), eiF32(tc.scale)...)
			if tc.truncate {
				data = data[:16]
			}
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: data}); err == nil {
				t.Fatalf("accepted invalid region %+v", tc)
			}
			if len(sender.devices[42].regions) != 0 {
				t.Fatal("invalid region was retained")
			}
		})
	}
}

func TestEISetupEventRejectsTruncatedDeviceAndInterfaceReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"missing device id", nil},
		{"missing interface version", eiU64(42)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: 11, devices: make(map[uint64]*eiDevice)}
			if err := sender.consumeSetupEvent(eiMessage{object: 11, opcode: 4, body: tc.body}); err == nil {
				t.Fatal("accepted a truncated device announcement")
			}
			if len(sender.devices) != 0 {
				t.Fatalf("truncated device announcement mutated devices: %+v", sender.devices)
			}
		})
	}

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"missing interface object", []byte{1}},
		{"truncated interface name", append(eiU64(72), eiU32(2)...)},
		{"invalid UTF-8 interface name", append(eiU64(72), eiString(string([]byte{0xff}))...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: make(map[string]uint64)}}}
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 5, body: tc.body}); err == nil {
				t.Fatal("accepted a malformed device interface reference")
			}
			if len(sender.devices[42].interfaces) != 0 {
				t.Fatalf("malformed interface reference was retained: %+v", sender.devices[42].interfaces)
			}
		})
	}

	sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: make(map[string]uint64)}}}
	if err := sender.consumeSetupEvent(eiMessage{object: 99, opcode: 5, body: append(eiU64(73), eiString("ei_keyboard")...)}); err != nil {
		t.Fatalf("unannounced device interface: %v", err)
	}
	if len(sender.devices) != 1 || len(sender.devices[42].interfaces) != 0 {
		t.Fatalf("foreign object changed negotiated device state: %+v", sender.devices)
	}
}

func TestEISetupEventRejectsGeometryTruncatedAtEveryField(t *testing.T) {
	geometry := append(append(append(append(eiU32(1), eiU32(2)...), eiU32(30)...), eiU32(40)...), eiF32(1)...)
	for _, tc := range []struct {
		name string
		size int
	}{
		{"x", 0},
		{"y", 4},
		{"width", 8},
		{"height", 12},
		{"scale", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: make(map[string]uint64)}}}
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: geometry[:tc.size]}); err == nil {
				t.Fatalf("accepted geometry truncated at %s", tc.name)
			}
			if len(sender.devices[42].regions) != 0 {
				t.Fatalf("truncated geometry published regions: %+v", sender.devices[42].regions)
			}
		})
	}
}

func TestEISetupEventCanPauseAResumedDevice(t *testing.T) {
	device := &eiDevice{id: 42, resumed: true, interfaces: map[string]uint64{"ei_keyboard": 73}}
	sender := &eiSender{devices: map[uint64]*eiDevice{42: device}}
	if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 8}); err != nil {
		t.Fatalf("device pause event: %v", err)
	}
	if device.resumed {
		t.Fatal("paused device remained eligible for input selection")
	}
}

func TestEIRequestPadsProtocolBodiesAndTextFramesRejectUnavailableInput(t *testing.T) {
	request := eiRequest(0x42, 3, []byte{1, 2, 3})
	if len(request) != eiHeaderSize+4 || binary.NativeEndian.Uint32(request[8:12]) != uint32(len(request)) || request[eiHeaderSize] != 1 || request[eiHeaderSize+1] != 2 || request[eiHeaderSize+2] != 3 || request[eiHeaderSize+3] != 0 {
		t.Fatalf("unaligned EIS request = %v", request)
	}

	if frames, err := eiTextFrames(&eiSender{}, ""); err != nil || len(frames) != 0 {
		t.Fatalf("empty text on an unavailable source = %v, %v; want no-op", frames, err)
	}
	if _, err := eiTextFrames(&eiSender{}, "text"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("text without negotiated capability = %v, want unsupported", err)
	}
	if _, err := eiTextFrames(&eiSender{text: 1, textDev: 2, client: &eiClient{}}, string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 text reached the EIS encoder")
	}
}

func TestEISenderCancellationBeforeHandshakeClosesItsClient(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := setupEISender(ctx, clientConn); !errors.Is(err, context.Canceled) {
		t.Fatalf("setup with canceled caller = %v, want context.Canceled", err)
	}
	if _, err := clientConn.Write([]byte{1}); err == nil {
		t.Fatal("failed EIS setup left its client connection open")
	}
}

type eiObservedWaitContext struct {
	context.Context
	waits chan uint32
	count atomic.Uint32
}

func (c *eiObservedWaitContext) Done() <-chan struct{} {
	c.waits <- c.count.Add(1)
	return c.Context.Done()
}

func awaitEISetupWait(t *testing.T, ctx *eiObservedWaitContext, want uint32) {
	t.Helper()
	for {
		select {
		case got := <-ctx.waits:
			if got == want {
				return
			}
			if got > want {
				t.Fatalf("EIS setup reached wait %d before expected wait %d", got, want)
			}
		case <-ctx.Context.Done():
			t.Fatalf("EIS setup context ended before wait %d: %v", want, ctx.Context.Err())
		case <-time.After(2 * time.Second):
			t.Fatalf("EIS setup did not reach wait %d", want)
		}
	}
}

func startObservedEISenderSetup(t *testing.T) (*net.UnixConn, *eiObservedWaitContext, context.CancelFunc, <-chan error) {
	t.Helper()
	clientConn, serverConn := unixConnectionPair(t)
	t.Cleanup(func() { _ = serverConn.Close() })
	base, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	ctx := &eiObservedWaitContext{Context: base, waits: make(chan uint32, 16)}
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := setupEISender(ctx, clientConn)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		_ = serverConn.Close()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("EIS setup did not stop during test cleanup")
		}
	})
	return serverConn, ctx, cancel, result
}

func TestEISenderCancellationAtEachNegotiationWait(t *testing.T) {
	for _, stage := range []string{"connection", "seat", "capabilities", "device-enumeration", "resumed-device"} {
		t.Run(stage, func(t *testing.T) {
			server, ctx, cancel, result := startObservedEISenderSetup(t)
			awaitEISetupWait(t, ctx, 1)
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			awaitEISetupWait(t, ctx, 2)
			if stage == "connection" {
				cancel()
				assertEISSetupCanceled(t, result)
				return
			}

			const connectionID = uint64(0xff00000000000010)
			const pingID = uint64(0xff00000000000011)
			assertEISenderAnswersPing(t, server, connectionID, pingID)
			awaitEISetupWait(t, ctx, 3)
			if stage == "seat" {
				cancel()
				assertEISSetupCanceled(t, result)
				return
			}

			const seatID = uint64(0xff00000000000020)
			if _, err := server.Write(eiRequest(connectionID, 1, eiU64(seatID))); err != nil {
				t.Fatal(err)
			}
			awaitEISetupWait(t, ctx, 4)
			if stage == "capabilities" {
				cancel()
				assertEISSetupCanceled(t, result)
				return
			}

			for i, capability := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
				body := append(eiU64(1<<i), eiString(capability)...)
				if _, err := server.Write(eiRequest(seatID, 2, body)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := server.Write(eiRequest(seatID, 3, nil)); err != nil {
				t.Fatal(err)
			}
			bind := readEIRequest(t, server)
			if bind.object != seatID || bind.opcode != 1 {
				t.Fatalf("seat capability bind = %+v", bind)
			}
			synchronize := readEIRequest(t, server)
			if synchronize.object != connectionID || synchronize.opcode != 0 || len(synchronize.body) != 12 {
				t.Fatalf("device setup synchronization = %+v", synchronize)
			}
			callbackID := binary.NativeEndian.Uint64(synchronize.body)
			awaitEISetupWait(t, ctx, 5)
			if stage == "device-enumeration" {
				cancel()
				assertEISSetupCanceled(t, result)
				return
			}

			if _, err := server.Write(eiRequest(callbackID, 0, eiU64(0))); err != nil {
				t.Fatal(err)
			}
			awaitEISetupWait(t, ctx, 6)
			cancel()
			assertEISSetupCanceled(t, result)
		})
	}
}

func assertEISSetupCanceled(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled EIS setup = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled EIS setup did not return")
	}
}

func TestEIReadStringRejectsEmbeddedNUL(t *testing.T) {
	data := eiU32(6)
	data = append(data, []byte{'h', 'e', 0, 'l', 'o', 0}...)
	if value, err := eiReadString(data, 0); err == nil {
		t.Fatalf("accepted embedded NUL in protocol string: %q", value)
	}
}

func TestEIStringRoundtripRejectsTruncation(t *testing.T) {
	for _, value := range []string{"hello", "échéance", ""} {
		actual, err := eiReadString(eiString(value), 0)
		if err != nil || actual != value {
			t.Fatalf("string %q decoded as %q, %v", value, actual, err)
		}
	}
	for _, data := range [][]byte{{1, 2}, {1, 0, 0, 0, 'x'}, {2, 0, 0, 0, 'a', 'b'}} {
		if _, err := eiReadString(data, 0); err == nil {
			t.Fatalf("accepted malformed string %v", data)
		}
	}
}

func TestEIClientContextCancellation(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("next canceled: %v", err)
	}
}

func TestEIClientNextRejectsMissingContextAndClosedStream(t *testing.T) {
	client := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	if _, err := client.next(nil); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("next with nil context = %v", err)
	}
	close(client.done)
	if _, err := client.next(context.Background()); err == nil || !strings.Contains(err.Error(), "EIS stream closed") {
		t.Fatalf("next after clean stream close = %v", err)
	}
}

func TestEIAnswerPingPropagatesClosedTransport(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	client := &eiClient{conn: clientConn}
	message := eiMessage{body: append(eiU64(17), eiU32(1)...)}
	if err := client.answerPing(message); err == nil || !strings.Contains(err.Error(), "answer connection ping") {
		t.Fatalf("ping response on a closed transport = %v", err)
	}
}
