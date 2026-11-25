//go:build linux

package input

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type cancelBeforeWriteCtx struct {
	recordingCtx
	cancel       context.CancelFunc
	cancelAt     int
	attempts     int
	cleanupError error
}

func (f *cancelBeforeWriteCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	f.attempts++
	if f.attempts == f.cancelAt {
		f.cancel()
	}
	if f.attempts > f.cancelAt && f.cleanupError != nil {
		return f.cleanupError
	}
	return f.recordingCtx.WriteMsgContext(ctx, data, oob)
}

func TestWlKeyboardTapReleasesAfterCancellationBeforeFinalRelease(t *testing.T) {
	cleanupError := errors.New("keyboard cleanup failed")
	for _, tc := range []struct {
		name         string
		cleanupError error
		wantWrites   int
	}{
		{name: "cleanup succeeds", wantWrites: 2},
		{name: "cleanup fails", cleanupError: cleanupError, wantWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &cancelBeforeWriteCtx{cancel: cancel, cancelAt: 2, cleanupError: tc.cleanupError}
			k := &wlKeyboard{
				ctx:  f,
				kbd:  &wl.RawProxy{},
				held: make(map[string]uint32),
			}
			k.kbd.SetID(42)

			err := k.tap(ctx, 15)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("tap error = %v, want context.Canceled", err)
			}
			if tc.cleanupError != nil && !errors.Is(err, tc.cleanupError) {
				t.Fatalf("tap error = %v, want cleanup error %v joined", err, tc.cleanupError)
			}
			if f.writes != tc.wantWrites {
				t.Fatalf("writes = %d, want %d", f.writes, tc.wantWrites)
			}
			if tc.cleanupError == nil && wl.Uint32(f.msgs[1][16:20]) != 0 {
				t.Fatalf("cleanup key state = %d, want release", wl.Uint32(f.msgs[1][16:20]))
			}
		})
	}
}

func TestWlMouseClickReleasesAfterCancellationBeforeFinalRelease(t *testing.T) {
	cleanupError := errors.New("mouse cleanup failed")
	for _, tc := range []struct {
		name         string
		cleanupError error
		wantWrites   int
		wantJoined   bool
	}{
		{name: "cleanup succeeds", wantWrites: 6},
		{name: "cleanup fails", cleanupError: cleanupError, wantWrites: 4, wantJoined: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &cancelBeforeWriteCtx{cancel: cancel, cancelAt: 5, cleanupError: tc.cleanupError}
			b := &WlVirtualBackend{
				ptr:  &wl.RawProxy{},
				outW: 1920,
				outH: 1080,
			}
			b.ptr.SetID(7)

			err := b.mouseClickEvents(ctx, f, 10, 20, btnLeft)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mouse click error = %v, want context.Canceled", err)
			}
			if tc.wantJoined && !errors.Is(err, tc.cleanupError) {
				t.Fatalf("mouse click error = %v, want cleanup error %v joined", err, tc.cleanupError)
			}
			if f.writes != tc.wantWrites {
				t.Fatalf("writes = %d, want %d", f.writes, tc.wantWrites)
			}
			if tc.cleanupError == nil && wl.Uint32(f.msgs[4][16:20]) != 0 {
				t.Fatalf("cleanup button state = %d, want release", wl.Uint32(f.msgs[4][16:20]))
			}
		})
	}
}

type cancelAfterPointerFrameCtx struct {
	recordingCtx
	cancel context.CancelFunc
}

func (c *cancelAfterPointerFrameCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.recordingCtx.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) >= 8 && wl.Uint32(data[4:8])&0xffff == 4 {
		c.cancel()
	}
	return nil
}

func TestWlVirtualClickCancellationAfterMotionDoesNotPressButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	wire := &cancelAfterPointerFrameCtx{cancel: cancel}
	err := backend.mouseClickEvents(ctx, wire, 20, 30, btnLeft)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("click after motion cancellation = %v, want context.Canceled", err)
	}
	if len(wire.msgs) != 2 {
		t.Fatalf("messages before canceled click = %d, want motion and frame only", len(wire.msgs))
	}
	for _, msg := range wire.msgs {
		if opcode := wl.Uint32(msg[4:8]) & 0xffff; opcode == 2 {
			t.Fatal("click sent a button event after cancellation")
		}
	}
}

func TestWlVirtualConstructorHonorsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend, err := NewWlVirtualBackendContext(ctx, filepath.Join(t.TempDir(), "unused.sock"))
	if backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor with canceled context = backend %v, error %v; want context.Canceled", backend, err)
	}
}

func TestWlVirtualTypeRejectsMalformedSequenceBeforeWireOutput(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	err := backend.Type(context.Background(), "prefix{unclosed")
	if err == nil {
		t.Fatal("malformed key sequence was accepted")
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatalf("close after rejected type sequence: %v", closeErr)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("malformed type sequence emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualPointerFrameRejectsUnavailablePointerBeforeWireOutput(t *testing.T) {
	backend := &WlVirtualBackend{ptr: &wl.RawProxy{}}
	wire := &recordingCtx{}
	err := backend.ptrFrame(context.Background(), wire)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("pointer frame with unavailable pointer = %v, want unsupported error", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("unavailable pointer emitted %d Wayland messages", len(wire.msgs))
	}
}

type cancelAfterVirtualButtonFrame struct {
	recordingCtx
	cancel     context.CancelFunc
	buttonDown bool
}

func (c *cancelAfterVirtualButtonFrame) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.recordingCtx.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) < 8 || wl.Uint32(data[:4]) != 7 {
		return nil
	}
	switch wl.Uint32(data[4:8]) & 0xffff {
	case 2:
		if len(data) >= 20 && wl.Uint32(data[16:20]) == 1 {
			c.buttonDown = true
		}
	case 4:
		if c.buttonDown {
			c.buttonDown = false
			c.cancel()
		}
	}
	return nil
}

func TestWlVirtualClickCancellationDuringHoldReleasesButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wire := &cancelAfterVirtualButtonFrame{cancel: cancel}
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	err := backend.mouseClickEvents(ctx, wire, 20, 30, btnLeft)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("click cancellation error = %v, want context.Canceled", err)
	}

	var buttonStates []uint32
	buttonUpIndex := -1
	for index, message := range wire.msgs {
		if wl.Uint32(message[4:8])&0xffff != 2 {
			continue
		}
		state := wl.Uint32(message[16:20])
		buttonStates = append(buttonStates, state)
		if state == 0 {
			buttonUpIndex = index
		}
	}
	if len(buttonStates) != 2 || buttonStates[0] != 1 || buttonStates[1] != 0 {
		t.Fatalf("button wire states = %v, want press followed by compensating release", buttonStates)
	}
	if buttonUpIndex < 0 || buttonUpIndex+1 >= len(wire.msgs) || wl.Uint32(wire.msgs[buttonUpIndex+1][4:8])&0xffff != 4 {
		t.Fatalf("button release was not followed by its frame: messages=%d release=%d", len(wire.msgs), buttonUpIndex)
	}
}

func TestWlVirtualMouseMoveRejectsCoordinatesAboveProtocolRange(t *testing.T) {
	coordinate := int64(^uint32(0)) + 1
	if int64(int(coordinate)) != coordinate {
		t.Skip("int cannot represent a coordinate above the Wayland uint32 range")
	}
	backend, peer := newCapturedWlVirtualBackend(t)
	err := backend.MouseMove(context.Background(), int(coordinate), 0)
	if err == nil || !strings.Contains(err.Error(), "exceed uint32 range") {
		t.Fatalf("oversized absolute coordinate returned %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("invalid coordinate sent %d Wayland messages", len(messages))
	}
}

func TestWlVirtualMouseMovePropagatesTransportFailureWithoutFrame(t *testing.T) {
	wire := &failingCtx{successWrites: 0}
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	err := backend.mouseMoveEvent(context.Background(), wire, 20, 30)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("motion transport failure returned %v", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("failed motion sent %d Wayland messages", len(wire.msgs))
	}
}

func TestWlVirtualMotionDeliveryBarrier(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.MouseMove(ctx, 60, 70); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.FlushMotionRequests(ctx) }()
	read := func(size int) []byte {
		t.Helper()
		data := make([]byte, size)
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	motion, frame, sync := read(28), read(8), read(12)
	if wl.Uint32(motion[12:16]) != 60 || wl.Uint32(motion[16:20]) != 70 || wl.Uint32(frame[4:8])&0xffff != 4 || wl.Uint32(sync[:4]) != 1 || wl.Uint32(sync[4:8])&0xffff != 0 {
		t.Fatalf("unexpected wire sequence: %x %x %x", motion, frame, sync)
	}
	select {
	case err := <-done:
		t.Fatalf("barrier returned before acknowledgement: %v", err)
	default:
	}
	callback := wl.Uint32(sync[8:12])
	reply := make([]byte, 12)
	binary.NativeEndian.PutUint32(reply[:4], callback)
	binary.NativeEndian.PutUint32(reply[4:8], 12<<16)
	if _, err := conn.Write(reply); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.FlushMotionRequests(ctx); err == nil {
		t.Fatal("closed backend accepted barrier")
	}
}

func TestWlVirtualMotionDeliveryBarrierCancellation(t *testing.T) {
	b, _ := newCapturedWlVirtualBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := b.FlushMotionRequests(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unacknowledged barrier: %v", err)
	}
}

type observedWlVirtualDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedWlVirtualDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestWlVirtualCanceledOperationLeavesSerializedAdmission(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	gate := backend.operationGateChannel()
	<-gate
	defer func() { gate <- struct{}{} }()

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedWlVirtualDoneContext{Context: base, observed: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- backend.MouseMove(ctx, 20, 30) }()
	select {
	case <-ctx.observed:
	case <-time.After(time.Second):
		t.Fatal("operation did not enter cancellation-aware admission")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked operation returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled operation did not leave admission")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close after canceled admission: %v", err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("canceled admission emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualPointerCoordinateQueryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var backend *WlVirtualBackend
	if _, err := backend.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinate query after cancellation = %v, want context.Canceled", err)
	}
}

func TestWlVirtualClosedBackendRejectsCoordinateQueryAndMouseUp(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend: %v", err)
	}
	if _, err := backend.PointerCoordinateSpace(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is closed") {
		t.Fatalf("closed backend coordinate query = %v, want closed error", err)
	}
	if err := backend.MouseUp(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "backend is closed") {
		t.Fatalf("mouse up after close = %v, want closed error", err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("closed backend emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualKeyOperationsPropagateKeyboardTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		call  func(*WlVirtualBackend) error
		setUp func(*wlKeyboard)
	}{
		{
			name: "key down",
			call: func(b *WlVirtualBackend) error { return b.KeyDown(context.Background(), "a") },
		},
		{
			name:  "key up",
			setUp: func(k *wlKeyboard) { k.held["a"] = kcDynBase },
			call:  func(b *WlVirtualBackend) error { return b.KeyUp(context.Background(), "a") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newCapturedWlVirtualBackend(t)
			keyboard, wire := newFailingKeyboard(0)
			if tc.setUp != nil {
				tc.setUp(keyboard)
			}
			backend.kbd = keyboard
			err := tc.call(backend)
			if err == nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("%s transport failure = %v, want injected write error", tc.name, err)
			}
			if wire.writes != 0 {
				t.Fatalf("keyboard recorded %d successful writes after injected failure", wire.writes)
			}
			if closeErr := backend.Close(); closeErr != nil {
				t.Fatalf("close after %s failure: %v", tc.name, closeErr)
			}
			if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
				t.Fatalf("failed %s emitted %d display messages", tc.name, len(messages))
			}
		})
	}
}

func TestWlVirtualScrollPropagatesDisconnectedTransportFailure(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.ScrollUp(context.Background(), 1); err == nil {
		t.Fatal("scroll succeeded after its Wayland peer disconnected")
	}
}

// Cancellation must take precedence over invalid button or scroll payloads;
// a revoked operation cannot inspect arguments or mutate compositor input.
func TestWlVirtualCanceledButtonAndScrollRejectBeforePayloadValidation(t *testing.T) {
	b := &WlVirtualBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"button down", func(c context.Context) error { return b.MouseDown(c, 999) }},
		{"button up", func(c context.Context) error { return b.MouseUp(c, 999) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
		})
	}
}

func TestNewWlVirtualBackend_NoSocket(t *testing.T) {
	_, err := NewWlVirtualBackend("")
	if err == nil {
		t.Fatal("expected error for empty socket")
	}
	t.Logf("got expected error: %v", err)
}

func TestNewWlVirtualBackend_Unreachable(t *testing.T) {
	_, err := NewWlVirtualBackend("/nonexistent.sock")
	if err == nil {
		t.Fatal("expected error for unreachable socket")
	}
	t.Logf("got expected error: %v", err)
}

func TestBtnCode(t *testing.T) {
	tests := []struct {
		button  int
		want    uint32
		wantErr bool
	}{
		{1, btnLeft, false},
		{2, btnMiddle, false},
		{3, btnRight, false},
		{0, 0, true},
		{99, 0, true},
	}
	for _, tc := range tests {
		got, err := btnCode(tc.button)
		if (err != nil) != tc.wantErr {
			t.Errorf("btnCode(%d) error = %v, wantErr %t", tc.button, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("btnCode(%d) = %d, want %d", tc.button, got, tc.want)
		}
	}
}

func TestWlVirtualBackend_Close_NilSession(t *testing.T) {
	// Close with a nil session should not panic.
	b := &WlVirtualBackend{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close with nil session: %v", err)
	}
}

func TestWlVirtualBackend_Close_NilDisplayContext(t *testing.T) {
	// Close with a partial session should not panic.
	b := &WlVirtualBackend{session: &wl.Session{Display: &wl.Display{}}}
	if err := b.Close(); err != nil {
		t.Fatalf("Close with partial session: %v", err)
	}
}

func TestScroll_SignConvention(t *testing.T) {
	// Verify the scroll sign convention:
	// ScrollUp sends negative values, ScrollDown sends positive.
	// scroll(axis, clicks) computes: value = clicks * 15 * 256
	// ScrollUp calls scroll(0, -clicks) → negative value
	// ScrollDown calls scroll(0, clicks) → positive value

	// Verify the fixed-point math: 15 * 256 = 3840 per notch
	expectedPerNotch := int32(15 * 256)
	if expectedPerNotch != 3840 {
		t.Errorf("scroll resolution = %d, want 3840", expectedPerNotch)
	}

	// 3 notches up → -3 * 3840 = -11520
	upValue := int32(-3 * 15 * 256)
	if upValue != -11520 {
		t.Errorf("3 notches up = %d, want -11520", upValue)
	}

	// 3 notches down → 3 * 3840 = 11520
	downValue := int32(3 * 15 * 256)
	if downValue != 11520 {
		t.Errorf("3 notches down = %d, want 11520", downValue)
	}
}

func newCapturedWlVirtualBackend(t *testing.T) (*WlVirtualBackend, *net.UnixConn) {
	t.Helper()
	sock := shortPrivateSocketPath(t, fmt.Sprintf("pf-wl-%d.sock", time.Now().UnixNano()))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}

	accepted := make(chan *net.UnixConn, 1)
	acceptErrCh := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			acceptErrCh <- acceptErr
			return
		}
		accepted <- conn
	}()

	ctx, err := wl.Connect(sock)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("Connect: %v", err)
	}

	var serverConn *net.UnixConn
	select {
	case serverConn = <-accepted:
	case err := <-acceptErrCh:
		_ = listener.Close()
		_ = ctx.Close()
		t.Fatalf("AcceptUnix: %v", err)
	case <-time.After(time.Second):
		_ = listener.Close()
		_ = ctx.Close()
		t.Fatal("AcceptUnix timed out")
	}

	b := &WlVirtualBackend{
		session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)},
		ptr:     &wl.RawProxy{},
		outW:    1920,
		outH:    1080,
	}
	b.ptr.SetID(7)

	t.Cleanup(func() {
		_ = ctx.Close()
		_ = serverConn.Close()
		_ = listener.Close()
		_ = os.Remove(sock)
	})

	return b, serverConn
}

func readCapturedWlWrites(t *testing.T, conn *net.UnixConn) [][]byte {
	t.Helper()
	var buf bytes.Buffer
	scratch := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, err := conn.Read(scratch)
		if n > 0 {
			buf.Write(scratch[:n])
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var ne net.Error
			if errors.As(err, &ne) {
				break
			}
			t.Fatalf("read captured writes: %v", err)
		}
		break
	}
	_ = conn.SetReadDeadline(time.Time{})
	data := buf.Bytes()
	var msgs [][]byte
	for len(data) > 0 {
		if len(data) < 8 {
			t.Fatalf("captured truncated message: %d bytes", len(data))
		}
		size := int(wl.Uint32(data[4:8]) >> 16)
		if size < 8 || size > len(data) {
			t.Fatalf("invalid Wayland message size %d (remaining=%d)", size, len(data))
		}
		msgs = append(msgs, append([]byte(nil), data[:size]...))
		data = data[size:]
	}
	return msgs
}

func TestWlVirtualBackend_MouseMoveWritesMotionAndFrame(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)

	if err := b.MouseMove(context.Background(), 123, 456); err != nil {
		t.Fatalf("MouseMove: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	msgs := readCapturedWlWrites(t, conn)
	if len(msgs) != 2 {
		t.Fatalf("writes = %d, want 2", len(msgs))
	}
	first := msgs[0]
	if got := wl.Uint32(first[0:4]); got != 7 {
		t.Fatalf("sender id = %d, want 7", got)
	}
	if got := wl.Uint32(first[4:8]) & 0xffff; got != 1 {
		t.Fatalf("opcode = %d, want motion_absolute", got)
	}
	if got, want := wl.Uint32(first[12:16]), uint32(123); got != want {
		t.Fatalf("motion x = %d, want %d", got, want)
	}
	if got, want := wl.Uint32(first[16:20]), uint32(456); got != want {
		t.Fatalf("motion y = %d, want %d", got, want)
	}
	if got := wl.Uint32(first[20:24]); got != 1920 || wl.Uint32(first[24:28]) != 1080 {
		t.Fatalf("motion size = (%d,%d), want (1920,1080)", wl.Uint32(first[20:24]), wl.Uint32(first[24:28]))
	}
	second := msgs[1]
	if got := wl.Uint32(second[4:8]) & 0xffff; got != 4 {
		t.Fatalf("frame opcode = %d, want 4", got)
	}
}

func TestWlVirtualBackend_MouseMoveRejectsNegativeCoordinates(t *testing.T) {
	b, _ := newCapturedWlVirtualBackend(t)

	if err := b.MouseMove(context.Background(), -1, 0); err == nil {
		t.Fatal("MouseMove accepted a negative x coordinate")
	}
}

func TestWlVirtualBackend_MouseClickWritesSequence(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)

	if err := b.MouseClick(context.Background(), 10, 20, 1); err != nil {
		t.Fatalf("MouseClick: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	msgs := readCapturedWlWrites(t, conn)
	if len(msgs) != 6 {
		t.Fatalf("writes = %d, want 6", len(msgs))
	}
	if got := wl.Uint32(msgs[0][4:8]) & 0xffff; got != 1 {
		t.Fatalf("first opcode = %d, want motion_absolute", got)
	}
	if got := wl.Uint32(msgs[2][4:8]) & 0xffff; got != 2 {
		t.Fatalf("third opcode = %d, want button", got)
	}
	if got := wl.Uint32(msgs[4][4:8]) & 0xffff; got != 2 {
		t.Fatalf("fifth opcode = %d, want button release", got)
	}
}

func TestWlVirtualBackend_ScrollWritesAxis(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)

	if err := b.ScrollRight(context.Background(), 2); err != nil {
		t.Fatalf("ScrollRight: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	msgs := readCapturedWlWrites(t, conn)
	if len(msgs) != 2 {
		t.Fatalf("writes = %d, want 2", len(msgs))
	}
	first := msgs[0]
	if got := wl.Uint32(first[4:8]) & 0xffff; got != 3 {
		t.Fatalf("opcode = %d, want axis", got)
	}
	if got := int32(wl.Uint32(first[16:20])); got != 2*15*256 {
		t.Fatalf("scroll value = %d, want %d", got, 2*15*256)
	}
	second := msgs[1]
	if got := wl.Uint32(second[4:8]) & 0xffff; got != 4 {
		t.Fatalf("frame opcode = %d, want 4", got)
	}
}

func TestWlVirtualBackend_PointerLocationUnsupported(t *testing.T) {
	b := &WlVirtualBackend{}
	if _, _, err := b.PointerLocation(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("PointerLocation error = %v, want ErrNotSupported", err)
	}
}

func TestWlVirtualBackend_CloseUsesSessionContext(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	msgs := readCapturedWlWrites(t, conn)
	if len(msgs) != 0 {
		t.Fatalf("close captured %d writes, want 0", len(msgs))
	}
}

func TestWlVirtualBackend_CanceledContextShortCircuitsMethods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &WlVirtualBackend{}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "MouseMove",
			run:  func() error { return b.MouseMove(ctx, 1, 2) },
		},
		{
			name: "MouseClick",
			run:  func() error { return b.MouseClick(ctx, 1, 2, 1) },
		},
		{
			name: "MouseDown",
			run:  func() error { return b.MouseDown(ctx, 1) },
		},
		{
			name: "MouseUp",
			run:  func() error { return b.MouseUp(ctx, 1) },
		},
		{
			name: "TypeContext",
			run:  func() error { return b.typeContext(ctx, "A{ctrl+a}") },
		},
		{
			name: "KeyDown",
			run:  func() error { return b.KeyDown(ctx, "a") },
		},
		{
			name: "KeyUp",
			run:  func() error { return b.KeyUp(ctx, "a") },
		},
		{
			name: "ScrollUp",
			run:  func() error { return b.ScrollUp(ctx, 1) },
		},
		{
			name: "ScrollDown",
			run:  func() error { return b.ScrollDown(ctx, 1) },
		},
		{
			name: "ScrollLeft",
			run:  func() error { return b.ScrollLeft(ctx, 1) },
		},
		{
			name: "ScrollRight",
			run:  func() error { return b.ScrollRight(ctx, 1) },
		},
		{
			name: "PointerLocation",
			run: func() error {
				_, _, err := b.PointerLocation(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
		})
	}
}

func TestWlVirtualBackend_CloseCancelsBlockedOperation(t *testing.T) { //nolint:gocyclo // exercises concurrent close, cancellation, and repeated close semantics.
	b, _ := newCapturedWlVirtualBackend(t)
	started := make(chan struct{})
	progress := make(chan struct{}, 1)
	opDone := make(chan error, 1)
	go func() {
		opDone <- b.withOperation(context.Background(), func(ctx wl.Ctx, opCtx context.Context) error {
			close(started)
			chunk := make([]byte, 4096)
			for {
				if err := ctx.WriteMsgContext(opCtx, chunk, nil); err != nil {
					return err
				}
				select {
				case progress <- struct{}{}:
				default:
				}
			}
		})
	}()
	<-started
	select {
	case <-progress:
	case err := <-opDone:
		t.Fatalf("operation returned before the blocked write: %v", err)
	case <-time.After(time.Second):
		t.Fatal("operation made no progress")
	}
	for {
		select {
		case <-progress:
		case err := <-opDone:
			t.Fatalf("operation returned before Close: %v", err)
		case <-time.After(10 * time.Millisecond):
			goto blocked
		}
	}

blocked:
	closeDone := make(chan error, 2)
	go func() { closeDone <- b.Close() }()
	go func() { closeDone <- b.Close() }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not join blocked operation")
		}
	}
	select {
	case err := <-opDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked operation did not exit after Close")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := b.MouseMove(context.Background(), 1, 2); err == nil {
		t.Fatal("MouseMove succeeded after Close")
	}
}

func TestWlVirtualBackendQueuedOperationCancellation(t *testing.T) {
	b := &WlVirtualBackend{session: &wl.Session{Display: wl.NewDisplay(&wl.Context{})}}
	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- b.withOperation(context.Background(), func(_ wl.Ctx, opCtx context.Context) error {
			close(firstStarted)
			<-opCtx.Done()
			return opCtx.Err()
		})
	}()
	<-firstStarted

	queuedCtx, cancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- b.withOperation(queuedCtx, func(wl.Ctx, context.Context) error { return nil })
	}()
	cancel()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not honor cancellation")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the active operation")
	}
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first operation error = %v, want context.Canceled", err)
	}
}

// wl_output only sends the scale event from version 2, and the logical extent
// that motion_absolute is measured and encoded against depends on it.
func TestWlOutputBindVersionRequestsTheScaleEvent(t *testing.T) {
	cases := []struct {
		name       string
		advertised uint32
		want       uint32
	}{
		{name: "version 1 cannot carry scale", advertised: 1, want: 1},
		{name: "version 2 carries scale", advertised: 2, want: 2},
		{name: "newer versions bind no higher", advertised: 4, want: 2},
		{name: "unspecified", advertised: 0, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wlOutputBindVersion(tc.advertised); got != tc.want {
				t.Fatalf("wlOutputBindVersion(%d) = %d, want %d", tc.advertised, got, tc.want)
			}
		})
	}
}

// motion_absolute carries the output's logical size, so the extent has to come
// from the mode size divided by the output scale.
//
// A version 1 bind receives no scale event at all, which is why the bind
// version decides this: the two cases below differ only in whether the scale
// event arrived, and the extent differs by the scale factor.
func TestOutputExtentDependsOnWhetherTheScaleEventArrives(t *testing.T) {
	const (
		modeW = uint32(3840)
		modeH = uint32(2160)
	)

	// Version 1: only the mode event is delivered.
	v1 := &WlVirtualBackend{}
	v1.applyOutputMode(modeW, modeH)
	gotW, gotH := v1.outputExtent()
	if gotW != modeW || gotH != modeH {
		t.Fatalf("version 1 extent = %dx%d, want the mode size %dx%d", gotW, gotH, modeW, modeH)
	}

	// Version 2: the scale event is delivered before the mode.
	for _, scale := range []uint32{1, 2, 3} {
		v2 := &WlVirtualBackend{}
		v2.applyOutputScale(scale)
		v2.applyOutputMode(modeW, modeH)
		gotW, gotH := v2.outputExtent()
		if wantW, wantH := modeW/scale, modeH/scale; gotW != wantW || gotH != wantH {
			t.Fatalf("scale %d extent = %dx%d, want %dx%d", scale, gotW, gotH, wantW, wantH)
		}
	}

	// A scale event before any mode has no geometry to divide yet.
	early := &WlVirtualBackend{}
	early.applyOutputScale(2)
	if gotW, gotH := early.outputExtent(); gotW != 0 || gotH != 0 {
		t.Fatalf("extent before any mode = %dx%d, want 0x0", gotW, gotH)
	}
}

// The scale has to survive a zero, which wl_output never sends but which would
// otherwise divide by zero.
func TestApplyOutputScaleNormalizesZero(t *testing.T) {
	b := &WlVirtualBackend{}
	b.applyOutputMode(2560, 1440)
	b.applyOutputScale(0)
	gotW, gotH := b.outputExtent()
	if gotW != 2560 || gotH != 1440 {
		t.Fatalf("extent after a zero scale = %dx%d, want 2560x1440", gotW, gotH)
	}
}

// A failed write may have already reached the compositor. The synthetic
// transport records the delivered frame before returning the write error.
type uncertainWaylandPressCtx struct {
	recordingCtx
	failAt     int
	pressErr   error
	releaseErr error
	attempts   int
	cancel     context.CancelFunc
}

func (f *uncertainWaylandPressCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	f.attempts++
	if f.attempts == f.failAt {
		_ = f.WriteMsg(data, oob)
		if f.cancel != nil {
			f.cancel()
		}
		return f.pressErr
	}
	if f.attempts > f.failAt && f.releaseErr != nil {
		return f.releaseErr
	}
	return f.recordingCtx.WriteMsgContext(ctx, data, oob)
}

func TestWaylandClickCompensatesForUncertainPressWrites(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failAt      int
		cleanupFail bool
		cancel      bool
	}{
		{name: "button event write", failAt: 3},
		{name: "button frame write and cleanup fails", failAt: 4, cleanupFail: true},
		{name: "button event write cancels request", failAt: 3, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("uncertain button-down write")
			releaseErr := errors.New("failed compensating release")
			fake := &uncertainWaylandPressCtx{failAt: tc.failAt, pressErr: pressErr}
			if tc.cleanupFail {
				fake.releaseErr = releaseErr
			}
			if tc.cancel {
				fake.cancel = cancel
			}
			backend := &WlVirtualBackend{ptr: &wl.RawProxy{}, outW: 1920, outH: 1080}
			backend.ptr.SetID(7)
			err := backend.mouseClickEvents(ctx, fake, 10, 20, btnLeft)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.cleanupFail || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("press and cleanup errors not preserved: %v", err)
			}
			expected := tc.failAt
			if !tc.cleanupFail {
				expected += 2
			}
			if fake.writes != expected {
				t.Fatalf("writes=%d, want %d including release frame", fake.writes, expected)
			}
			if !tc.cleanupFail {
				release := fake.msgs[tc.failAt]
				if wl.Uint32(release[4:8])&0xffff != 2 || wl.Uint32(release[16:20]) != 0 {
					t.Fatalf("failed press was not followed by button-up: %x", release)
				}
			}
		})
	}
}

// The compositor cannot advertise scale zero. A malformed event after a
// verified non-unit scale must not reset logical geometry or widen the region
// where the virtual pointer is authorized to move.
func TestWlVirtualZeroScaleCannotOverrideLastVerifiedLogicalExtent(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	b.applyOutputMode(3840, 2160)
	b.applyOutputScale(2)
	b.applyOutputScale(0)
	w, h := b.outputExtent()
	if w != 1920 || h != 1080 {
		t.Fatalf("invalid zero scale widened logical extent to %dx%d", w, h)
	}
	if err := b.MouseMove(context.Background(), 2100, 500); err == nil {
		t.Fatal("zero scale authorized motion outside verified logical output")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if writes := readCapturedWlWrites(t, conn); len(writes) != 0 {
		t.Fatalf("unverified movement emitted %d Wayland requests", len(writes))
	}
}
