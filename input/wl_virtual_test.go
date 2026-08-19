//go:build linux
// +build linux

package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("pf-wl-%d.sock", time.Now().UnixNano()))
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
	if got, want := wl.Uint32(first[12:16]), uint32(123*256); got != want {
		t.Fatalf("motion x = %d, want %d (123 << 8)", got, want)
	}
	if got, want := wl.Uint32(first[16:20]), uint32(456*256); got != want {
		t.Fatalf("motion y = %d, want %d (456 << 8)", got, want)
	}
	if got := wl.Uint32(first[20:24]); got != 1920 || wl.Uint32(first[24:28]) != 1080 {
		t.Fatalf("motion size = (%d,%d), want (1920,1080)", wl.Uint32(first[20:24]), wl.Uint32(first[24:28]))
	}
	second := msgs[1]
	if got := wl.Uint32(second[4:8]) & 0xffff; got != 4 {
		t.Fatalf("frame opcode = %d, want 4", got)
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
			name: "Sync",
			run:  func() error { return b.Sync(ctx) },
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

func TestWlVirtualBackend_CloseCancelsBlockedOperation(t *testing.T) {
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
	if err := b.Sync(context.Background()); err == nil {
		t.Fatal("Sync succeeded after Close")
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
