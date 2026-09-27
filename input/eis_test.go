package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

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
	case <-writeDone:
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
	path := filepath.Join(t.TempDir(), "eis.sock")
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
