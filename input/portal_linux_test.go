//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"golang.org/x/sys/unix"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errSyntheticEISClock = errors.New("synthetic monotonic clock failure")

type portalEISClockFault struct {
	calls  atomic.Int32
	failAt atomic.Int32
}

func (c *portalEISClockFault) get(clockID int32, ts *unix.Timespec) error {
	if c.calls.Add(1) == c.failAt.Load() {
		return errSyntheticEISClock
	}
	return unix.ClockGettime(clockID, ts)
}

func (c *portalEISClockFault) failNext(offset int32) {
	c.failAt.Store(c.calls.Load() + offset)
}

func (c *portalEISClockFault) clearFailure() {
	c.failAt.Store(0)
}

func TestRemoteDesktopTypeAbortsFrameBatchWhenMonotonicClockFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sequence  string
		failAt    int32
		startHeld bool
		wantHeld  bool
	}{
		{name: "temporary modifier press", sequence: "{ctrl+a}", failAt: 1},
		{name: "temporary modifier release", sequence: "{ctrl+a}", failAt: 2},
		{name: "tap press", sequence: "{a}", failAt: 1},
		{name: "tap release", sequence: "{a}", failAt: 2},
		{name: "explicit press", sequence: "{a down}", failAt: 1},
		{name: "explicit release", sequence: "{a up}", failAt: 1, startHeld: true, wantHeld: true},
		{name: "text frame", sequence: "private prefix", failAt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newWireObservedRemoteDesktop(t)
			clock := &portalEISClockFault{}
			backend.sender.clockGettime = clock.get
			if tc.startHeld {
				if err := backend.KeyDown(context.Background(), "a"); err != nil {
					t.Fatalf("prepare caller-owned key: %v", err)
				}
				assertEISKeyEventPair(t, peer, backend.sender.keyboard, backend.sender.keyboardDev, true)
			}

			clock.failNext(tc.failAt)
			err := backend.Type(context.Background(), tc.sequence)
			clock.clearFailure()
			if !errors.Is(err, errSyntheticEISClock) {
				t.Fatalf("Type(%q) error = %v, want clock failure", tc.sequence, err)
			}
			assertNoEIWireRequest(t, peer)
			keycode, _ := remoteKeyCode("a")
			if got := backend.heldKeys[keycode]; got != tc.wantHeld {
				t.Fatalf("held key after failed Type = %v, want %v", got, tc.wantHeld)
			}
			if err := backend.stateError(); err != nil {
				t.Fatalf("frame construction failure retired the session: %v", err)
			}
		})
	}
}

func TestRemoteDesktopKeyOperationsRetainOwnershipWhenFrameClockFails(t *testing.T) {
	t.Run("failed down does not claim key", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		clock.failNext(1)
		err := backend.KeyDown(context.Background(), "a")
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("KeyDown error = %v, want clock failure", err)
		}
		assertNoEIWireRequest(t, peer)
		keycode, _ := remoteKeyCode("a")
		if backend.heldKeys[keycode] {
			t.Fatal("failed key-down claimed caller ownership")
		}
	})

	t.Run("failed up preserves key for retry", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		if err := backend.KeyDown(context.Background(), "a"); err != nil {
			t.Fatalf("prepare caller-owned key: %v", err)
		}
		assertEISKeyEventPair(t, peer, backend.sender.keyboard, backend.sender.keyboardDev, true)
		clock.failNext(1)
		err := backend.KeyUp(context.Background(), "a")
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("KeyUp error = %v, want clock failure", err)
		}
		keycode, _ := remoteKeyCode("a")
		if !backend.heldKeys[keycode] {
			t.Fatal("failed key-up discarded ownership before transmitting a release")
		}
		assertNoEIWireRequest(t, peer)
		if err := backend.KeyUp(context.Background(), "a"); err != nil {
			t.Fatalf("retry owned key release: %v", err)
		}
		assertEISKeyEventPair(t, peer, backend.sender.keyboard, backend.sender.keyboardDev, false)
		if backend.heldKeys[keycode] {
			t.Fatal("successful key-up retained caller ownership")
		}
	})
}

func TestRemoteDesktopOtherOperationsRejectFrameWhenMonotonicClockFails(t *testing.T) {
	t.Run("literal text", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		clock.failNext(1)
		err := backend.TypeLiteral(context.Background(), "private")
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("TypeLiteral error = %v, want clock failure", err)
		}
		assertNoEIWireRequest(t, peer)
	})

	t.Run("pointer move", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		backend.sender.pointers = []eiPointerDevice{{
			device: 701, pointer: 702,
			regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
		}}
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		clock.failNext(1)
		err := backend.MouseMove(context.Background(), 20, 30)
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("MouseMove error = %v, want clock failure", err)
		}
		assertNoEIWireRequest(t, peer)
		if backend.hasPointer {
			t.Fatal("failed pointer frame changed recorded location")
		}
	})

	t.Run("button down", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		clock.failNext(1)
		err := backend.MouseDown(context.Background(), 1)
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("MouseDown error = %v, want clock failure", err)
		}
		assertNoEIWireRequest(t, peer)
		if len(backend.buttons) != 0 {
			t.Fatalf("failed button frame changed held state: %v", backend.buttons)
		}
	})

	t.Run("button up remains retryable", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		if err := backend.MouseDown(context.Background(), 1); err != nil {
			t.Fatalf("prepare caller-owned button: %v", err)
		}
		assertEISButtonEventPair(t, peer, backend.sender.button, backend.sender.buttonDev, true)
		clock.failNext(1)
		err := backend.MouseUp(context.Background(), 1)
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("MouseUp error = %v, want clock failure", err)
		}
		if len(backend.buttons) != 1 {
			t.Fatalf("failed button-up lost held ownership: %v", backend.buttons)
		}
		assertNoEIWireRequest(t, peer)
		if err := backend.MouseUp(context.Background(), 1); err != nil {
			t.Fatalf("retry owned button release: %v", err)
		}
		assertEISButtonEventPair(t, peer, backend.sender.button, backend.sender.buttonDev, false)
		if len(backend.buttons) != 0 {
			t.Fatalf("successful button-up retained ownership: %v", backend.buttons)
		}
	})

	t.Run("scroll", func(t *testing.T) {
		backend, peer := newWireObservedRemoteDesktop(t)
		clock := &portalEISClockFault{}
		backend.sender.clockGettime = clock.get
		clock.failNext(1)
		err := backend.ScrollDown(context.Background(), 1)
		clock.clearFailure()
		if !errors.Is(err, errSyntheticEISClock) {
			t.Fatalf("ScrollDown error = %v, want clock failure", err)
		}
		assertNoEIWireRequest(t, peer)
	})
}

func TestRemoteDesktopClickFrameClockFailureDoesNotInventButtonTransitions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failAt      int32
		wantPointer bool
		wantButton  bool
	}{
		{name: "pointer frame", failAt: 1},
		{name: "button press frame", failAt: 2, wantPointer: true},
		{name: "button release frame", failAt: 3, wantPointer: true, wantButton: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newWireObservedRemoteDesktop(t)
			backend.sender.pointers = []eiPointerDevice{{
				device: 701, pointer: 702,
				regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
			}}
			clock := &portalEISClockFault{}
			backend.sender.clockGettime = clock.get
			clock.failNext(tc.failAt)
			err := backend.MouseClick(context.Background(), 20, 30, 1)
			clock.clearFailure()
			if !errors.Is(err, errSyntheticEISClock) {
				t.Fatalf("MouseClick error = %v, want clock failure", err)
			}

			if tc.wantPointer {
				assertEISPointerEventPair(t, peer, 702, 701)
			} else {
				assertNoEIWireRequest(t, peer)
			}
			if tc.wantButton {
				assertEISButtonEventPair(t, peer, backend.sender.button, backend.sender.buttonDev, true)
			} else if tc.wantPointer {
				assertNoEIWireRequest(t, peer)
			}
			if backend.hasPointer != tc.wantPointer {
				t.Fatalf("pointer ownership = %v, want %v", backend.hasPointer, tc.wantPointer)
			}
			if (len(backend.buttons) != 0) != tc.wantButton {
				t.Fatalf("button ownership = %v, want held=%v", backend.buttons, tc.wantButton)
			}
			if tc.wantButton {
				if err := backend.MouseUp(context.Background(), 1); err != nil {
					t.Fatalf("recover click release after clock failure: %v", err)
				}
				assertEISButtonEventPair(t, peer, backend.sender.button, backend.sender.buttonDev, false)
				if len(backend.buttons) != 0 {
					t.Fatalf("successful recovery retained button ownership: %v", backend.buttons)
				}
			}
		})
	}
}

func assertEISKeyEventPair(t *testing.T, peer *net.UnixConn, object, device uint64, pressed bool) {
	t.Helper()
	request, err := readSyntheticEISFrame(peer)
	if err != nil || request.object != object || request.opcode != 1 || len(request.body) != 8 {
		t.Fatalf("keyboard event = %+v, %v", request, err)
	}
	if got := binary.NativeEndian.Uint32(request.body[4:8]) == 1; got != pressed {
		t.Fatalf("keyboard pressed = %v, want %v", got, pressed)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != device || frame.opcode != 3 {
		t.Fatalf("keyboard frame = %+v, %v", frame, err)
	}
}

func assertEISButtonEventPair(t *testing.T, peer *net.UnixConn, object, device uint64, pressed bool) {
	t.Helper()
	request, err := readSyntheticEISFrame(peer)
	if err != nil || request.object != object || request.opcode != 1 || len(request.body) != 8 {
		t.Fatalf("button event = %+v, %v", request, err)
	}
	if got := binary.NativeEndian.Uint32(request.body[4:8]) == 1; got != pressed {
		t.Fatalf("button pressed = %v, want %v", got, pressed)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != device || frame.opcode != 3 {
		t.Fatalf("button frame = %+v, %v", frame, err)
	}
}

func assertEISPointerEventPair(t *testing.T, peer *net.UnixConn, object, device uint64) {
	t.Helper()
	request, err := readSyntheticEISFrame(peer)
	if err != nil || request.object != object || request.opcode != 1 || len(request.body) != 8 {
		t.Fatalf("pointer event = %+v, %v", request, err)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != device || frame.opcode != 3 {
		t.Fatalf("pointer frame = %+v, %v", frame, err)
	}
}

func TestRemoteDesktopASCIIKeyCaseAliasesEmitTheSameEISCode(t *testing.T) {
	for _, key := range []string{"a", "A"} {
		t.Run(key, func(t *testing.T) {
			backend, peer := newWireObservedRemoteDesktop(t)
			wantCode, err := remoteKeyCode("a")
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.KeyDown(context.Background(), key); err != nil {
				t.Fatalf("KeyDown(%q): %v", key, err)
			}
			request, err := readSyntheticEISFrame(peer)
			if err != nil || request.object != backend.sender.keyboard || request.opcode != 1 || len(request.body) != 8 {
				t.Fatalf("keyboard event = %+v, %v", request, err)
			}
			if got := binary.NativeEndian.Uint32(request.body[:4]); got != wantCode {
				t.Fatalf("KeyDown(%q) sent code %d, want lowercase alias code %d", key, got, wantCode)
			}
			if got := binary.NativeEndian.Uint32(request.body[4:]); got != 1 {
				t.Fatalf("KeyDown(%q) state = %d, want pressed", key, got)
			}
			if _, err := readSyntheticEISFrame(peer); err != nil {
				t.Fatalf("read key-down frame: %v", err)
			}
			if err := backend.KeyUp(context.Background(), key); err != nil {
				t.Fatalf("KeyUp(%q): %v", key, err)
			}
			request, err = readSyntheticEISFrame(peer)
			if err != nil || request.object != backend.sender.keyboard || request.opcode != 1 || len(request.body) != 8 ||
				binary.NativeEndian.Uint32(request.body[:4]) != wantCode || binary.NativeEndian.Uint32(request.body[4:]) != 0 {
				t.Fatalf("keyboard release = %+v, %v", request, err)
			}
			if _, err := readSyntheticEISFrame(peer); err != nil {
				t.Fatalf("read key-up frame: %v", err)
			}
			if len(backend.heldKeys) != 0 {
				t.Fatalf("case alias left key owned after release: %v", backend.heldKeys)
			}
		})
	}
}

// cancelAtErrCallContext controls a context transition at a known observation
// boundary without depending on goroutine scheduling or elapsed time.
type cancelAtErrCallContext struct {
	context.Context
	cancel          context.CancelFunc
	cancelOnErrCall uint32
	errCalls        atomic.Uint32
}

func (c *cancelAtErrCallContext) Err() error {
	if c.errCalls.Add(1) == c.cancelOnErrCall {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRemoteDesktopSubmitObservesCancellationAtSuccessfulWriterReceipt(t *testing.T) {
	backend := syntheticQueuedPortal()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, cancelOnErrCall: 2}
	result := make(chan error, 1)
	go func() {
		result <- backend.submit(ctx, func() error { return nil })
	}()

	var command remoteDesktopCommand
	select {
	case command = <-backend.commands:
	case <-time.After(time.Second):
		t.Fatal("submit did not admit its command")
	}
	if err := command.run(); err != nil {
		t.Fatalf("synthetic writer operation failed: %v", err)
	}
	command.done <- nil

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("successful writer receipt hid cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after the writer receipt")
	}
}

func TestRemoteDesktopTypeLiteralCanceledBeforeWriterDispatchSendsNoFrames(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	backend := newRemoteDesktopBackend(&eiSender{client: client, text: 701, textDev: 702}, &portalSession{})
	t.Cleanup(func() { _ = backend.Close() })

	base, cancel := context.WithCancel(context.Background())
	// The operation checks cancellation before admission, the submitter checks
	// again before queueing, and the writer checks before running the command.
	// Cancel at the command's own check so no EIS frame can be dispatched.
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, cancelOnErrCall: 4}
	result := make(chan error, 1)
	go func() { result <- backend.TypeLiteral(ctx, "must not be sent") }()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("TypeLiteral after writer cancellation = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TypeLiteral did not return after writer cancellation")
	}

	// A later serialized no-op confirms that the canceled command finished
	// before checking the socket for an unauthorized text frame.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	if err := backend.submit(checkCtx, func() error { return nil }); err != nil {
		t.Fatalf("writer did not continue after rejecting canceled text: %v", err)
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("local cancellation revoked the authorized session: %v", err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set EIS peer read deadline: %v", err)
	}
	buf := make([]byte, 256)
	n, err := serverConn.Read(buf)
	if n != 0 {
		t.Fatalf("canceled text emitted %d EIS bytes: %x", n, buf[:n])
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("EIS peer read error = %v, want no-frame deadline", err)
	}
}

// A click must not consume a button the caller explicitly holds for a drag.
// Reject before pointer movement or a second down/up pair reaches EIS.
func TestRemoteDesktopClickNeverReleasesCallerHeldButton(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	ctx := context.Background()
	if err := b.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatalf("initial button-down wire frame %d: %v", i, err)
		}
	}
	if !b.buttons[0x110] {
		t.Fatal("initial button-down state missing")
	}
	err := b.MouseClick(ctx, 30, 40, 1)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("click consumed the caller-held drag: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if !b.buttons[0x110] || b.hasPointer {
		t.Fatalf("rejected click changed held state or pointer: buttons=%v pointer=%v", b.buttons, b.hasPointer)
	}
	if stateErr := b.stateError(); stateErr != nil {
		t.Fatalf("local validation revoked session: %v", stateErr)
	}
	if err := b.MouseUp(ctx, 1); err != nil {
		t.Fatalf("caller cannot release held drag after rejection: %v", err)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != b.sender.button || frame.opcode != 1 || len(frame.body) < 8 || binary.NativeEndian.Uint32(frame.body[4:8]) != 0 {
		t.Fatalf("held drag release not transmitted: frame=%+v err=%v", frame, err)
	}
	if _, err := readSyntheticEISFrame(peer); err != nil {
		t.Fatalf("release sync absent: %v", err)
	}
	if len(b.buttons) != 0 {
		t.Fatalf("button still held after legitimate release: %v", b.buttons)
	}
	if err := b.MouseClick(ctx, 30, 40, 1); err != nil {
		t.Fatalf("non-held click incorrectly rejected: %v", err)
	}
}

// Different buttons remain independently owned: a right click while the left
// button is held must preserve the left drag across both EIS transitions.
func TestRemoteDesktopClickOtherButtonPreservesIndependentDrag(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	ctx := context.Background()
	if err := b.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.MouseClick(ctx, 15, 35, 3); err != nil {
		t.Fatalf("other-button click rejected: %v", err)
	}
	if !b.buttons[0x110] || b.buttons[0x111] || !b.hasPointer || b.pointerX != 15 || b.pointerY != 35 {
		t.Fatalf("other-button click changed drag or pointer: buttons=%v pointer=(%d,%d) valid=%v", b.buttons, b.pointerX, b.pointerY, b.hasPointer)
	}
	var rightDown, rightUp, leftUp int
	for i := 0; i < 6; i++ {
		frame, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("click wire event %d absent: %v", i, err)
		}
		if frame.object != b.sender.button || frame.opcode != 1 || len(frame.body) < 8 {
			continue
		}
		code := binary.NativeEndian.Uint32(frame.body[:4])
		down := binary.NativeEndian.Uint32(frame.body[4:8]) != 0
		if code == 0x111 {
			if down {
				rightDown++
			} else {
				rightUp++
			}
		}
		if code == 0x110 && !down {
			leftUp++
		}
	}
	if rightDown != 1 || rightUp != 1 || leftUp != 0 {
		t.Fatalf("unexpected EIS button transitions right=%d/%d left-up=%d", rightDown, rightUp, leftUp)
	}
	if err := b.MouseUp(ctx, 1); err != nil {
		t.Fatalf("left drag could not release after right click: %v", err)
	}
	if b.buttons[0x110] {
		t.Fatal("left remained held after explicit release")
	}
}

func TestRemoteDesktopMouseClickMovementFailureRevokesWithoutRecordingInput(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
	}}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.MouseClick(context.Background(), 20, 30, 1)
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("click movement failure = %v, want revoked EIS transport error", err)
	}
	if backend.hasPointer || len(backend.buttons) != 0 {
		t.Fatalf("failed click movement recorded input: pointer=%v buttons=%v", backend.hasPointer, backend.buttons)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed click movement left authorization active: %v", stateErr)
	}
}

// With a real private EIS Unix transport, a clean portal close must release
// every held input before sending a single device stop, even when keyboard,
// button and scroll interfaces all share the same EIS device.
func TestRemoteDesktopCloseReleasesHeldKeysAndButtonsAndStopsSharedDeviceOnce(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client := &eiClient{conn: conn, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	dev := eiServerIDFloor + 0x32
	sender := &eiSender{client: client, connection: eiServerIDFloor + 0x30, seat: eiServerIDFloor + 0x31, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: dev, buttonDev: dev, scrollDev: dev, textDev: dev}
	portal := &portalSession{owner: ":1.44", path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
	b := newRemoteDesktopBackend(sender, portal)
	defer b.Close()
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := b.MouseDown(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatalf("reading original input event %d: %v", i, err)
		}
	}
	if len(b.heldKeys) != 1 || len(b.buttons) != 1 {
		t.Fatalf("test did not establish held input: keys=%v buttons=%v", b.heldKeys, b.buttons)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	releaseKey, releaseButton, stopCount := 0, 0, 0
	for i := 0; i < 5; i++ {
		frame, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("reading close frame %d: %v", i, err)
		}
		switch {
		case frame.object == sender.keyboard && frame.opcode == 1 && len(frame.body) >= 8 && binary.NativeEndian.Uint32(frame.body[4:8]) == 0:
			releaseKey++
		case frame.object == sender.button && frame.opcode == 1 && len(frame.body) >= 8 && binary.NativeEndian.Uint32(frame.body[4:8]) == 0:
			releaseButton++
		case frame.object == dev && frame.opcode == 2:
			stopCount++
		case frame.object == dev && frame.opcode == 3:
			// Synchronized key/button release frame
		default:
			t.Fatalf("unexpected frame during portal close %+v", frame)
		}
	}
	if releaseKey != 1 || releaseButton != 1 || stopCount != 1 {
		t.Fatalf("unbalanced close traffic: key=%d button=%d device stop=%d", releaseKey, releaseButton, stopCount)
	}
	if len(b.heldKeys) != 0 || len(b.buttons) != 0 {
		t.Fatalf("released keys/buttons still recorded: keys=%v buttons=%v", b.heldKeys, b.buttons)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSyntheticEISFrame(peer); !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close emitted extra message or left writer alive: %v", err)
	}
}

func TestRemoteDesktopCloseReportsHeldInputReleaseCapabilityFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setHeld    func(*RemoteDesktopBackend)
		dropDevice func(*RemoteDesktopBackend)
		stillHeld  func(*RemoteDesktopBackend) bool
	}{
		{
			name: "keyboard",
			setHeld: func(b *RemoteDesktopBackend) {
				b.heldKeys[30] = true
			},
			dropDevice: func(b *RemoteDesktopBackend) {
				b.sender.keyboardDev = 0
			},
			stillHeld: func(b *RemoteDesktopBackend) bool {
				return b.heldKeys[30]
			},
		},
		{
			name: "pointer button",
			setHeld: func(b *RemoteDesktopBackend) {
				b.buttons[0x110] = true
			},
			dropDevice: func(b *RemoteDesktopBackend) {
				b.sender.buttonDev = 0
			},
			stillHeld: func(b *RemoteDesktopBackend) bool {
				return b.buttons[0x110]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, _, _ := newSyntheticRevocableRemoteDesktop(t)
			tc.setHeld(backend)
			tc.dropDevice(backend)

			err := backend.Close()
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("Close error = %v, want missing-device capability error", err)
			}
			if !tc.stillHeld(backend) {
				t.Fatal("Close discarded held-input ownership after release failed")
			}
			if repeatedErr := backend.Close(); !errors.Is(repeatedErr, ErrNotSupported) {
				t.Fatalf("repeated Close error = %v, want the original cleanup failure", repeatedErr)
			}
		})
	}
}

// A second explicit KeyDown must not inject a repeat press for a key already
// owned by this EIS backend. This must be enforced on the serialized writer.
func TestRemoteDesktopRepeatedDirectKeyDownRetainsSingleOwnedPress(t *testing.T) {
	for _, key := range []string{"a", "enter", "ctrl", "f5"} {
		t.Run(key, func(t *testing.T) {
			assertDirectKeyDownOwnership(t, key)
		})
	}
}

func assertDirectKeyDownOwnership(t *testing.T, key string) {
	t.Helper()
	b, peer := newWireObservedRemoteDesktop(t)
	ctx := context.Background()
	if err := b.KeyDown(ctx, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	code, err := remoteKeyCode(key)
	if err != nil {
		t.Fatal(err)
	}
	if !b.heldKeys[code] {
		t.Fatal("first down not recorded")
	}
	err = b.KeyDown(ctx, key)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("duplicate key down accepted: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if !b.heldKeys[code] || len(b.heldKeys) != 1 {
		t.Fatalf("denied duplicate changed ownership: %v", b.heldKeys)
	}
	if err := b.KeyUp(ctx, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("release after denied duplicate failed: %v", b.heldKeys)
	}
	if err := b.KeyDown(ctx, key); err != nil {
		t.Fatalf("legitimate repress failed: %v", err)
	}
}

func TestRemoteDesktopDirectKeyDownReportsMissingKeyboardAuthorityWithoutWireEffects(t *testing.T) {
	for _, disable := range []string{"keyboard interface", "keyboard device"} {
		t.Run(disable, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if disable == "keyboard interface" {
				b.sender.keyboard = 0
			} else {
				b.sender.keyboardDev = 0
			}
			err := b.KeyDown(context.Background(), "a")
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unavailable keyboard did not reject with capability error: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("unavailable keyboard claimed held keys: %+v", b.heldKeys)
			}
		})
	}
}

// A repeated down on an already held EIS button must not synthesize an
// additional press. That press can reset or terminate drag recognition in a
// compositor, and it is not an independent action by the caller.
func TestRemoteDesktopDuplicateMouseDownDoesNotSendExtraEISFrames(t *testing.T) {
	for _, button := range []int{1, 2, 3} {
		t.Run([]string{"left", "middle", "right"}[button-1], func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			ctx := context.Background()
			if err := b.MouseDown(ctx, button); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatal(err)
				}
			}
			err := b.MouseDown(ctx, button)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate mouse down accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			code, err := remoteButtonCode(button)
			if err != nil {
				t.Fatal(err)
			}
			if !b.buttons[code] || len(b.buttons) != 1 {
				t.Fatalf("duplicate down changed existing drag: %v", b.buttons)
			}
			if err := b.MouseUp(ctx, button); err != nil {
				t.Fatalf("original drag cannot release: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.MouseDown(ctx, button); err != nil {
				t.Fatalf("repress after release failed: %v", err)
			}
		})
	}
}

// Transport failure is loss of the permission-bearing EIS session. A late
// caller must never see a successful input receipt or reuse that session.
func TestPortalEISSocketWriteFailureRevokesAuthorization(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	if err := b.write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("real socket could not send first frame: %v", err)
	}
	if err := client.conn.Close(); err != nil {
		t.Fatal(err)
	}
	err := b.write([]byte{5, 6, 7, 8})
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("broken EIS write did not revoke authorization: %v", err)
	}
	if got := b.stateError(); !errors.Is(got, ErrRemoteDesktopRevoked) {
		t.Fatalf("broken EIS backend remained authorized: %v", got)
	}
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("EIS failure did not notify authorization waiters")
	}
	if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked session accepted keyboard input: %v", err)
	}
	select {
	case <-b.writerDone:
	case <-time.After(time.Second):
		t.Fatal("writer remained active after transport loss")
	}
}

// Closing the event stream without a reader-supplied error is still terminal:
// an absent diagnostic is not evidence that permission remains valid.
func TestPortalEISReadTerminationWithoutCauseRevokesAuthorization(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	close(client.done)
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("ended EIS stream did not retire the session")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS stream closed") {
		t.Fatalf("missing cause allowed EIS stream to remain authoritative: %v", err)
	}
}

// A correctly authenticated Response signal cannot authorize the session if
// the original D-Bus method is still blocked when the caller cancels. The
// portal's late method reply must not be treated as successful consent.
func TestPortalRequestCancellationWhileMethodReplyIsPending(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.createMethodDelay = 250 * time.Millisecond
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	results, err := portalRequest(ctx, client, client.Names()[0], "CreateSession", []any{map[string]dbus.Variant{}})
	if !errors.Is(err, context.DeadlineExceeded) || results != nil {
		t.Fatalf("late portal method reply granted session: results=%+v err=%v", results, err)
	}
}

func TestRemoteDesktopPublicOperationAuthorizationFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disable  func(*eiSender)
		call     func(*RemoteDesktopBackend) error
		want     error
		contains string
	}{
		{"keyup without keyboard object", func(s *eiSender) { s.keyboard = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "a") }, ErrNotSupported, ""},
		{"keyup without keyboard device", func(s *eiSender) { s.keyboardDev = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "a") }, ErrNotSupported, ""},
		{"type literal without text device", func(s *eiSender) { s.textDev = 0 }, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), "private") }, ErrNotSupported, ""},
		{"type literal without text object", func(s *eiSender) { s.text = 0 }, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), "private") }, ErrNotSupported, ""},
		{"scroll without device", func(s *eiSender) { s.scrollDev = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollLeft(context.Background(), 2) }, ErrNotSupported, ""},
		{"scroll without interface", func(s *eiSender) { s.scroll = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollUp(context.Background(), 2) }, ErrNotSupported, ""},
		{"click without pointer", func(s *eiSender) { s.pointers = nil }, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 10, 11, 2) }, ErrNotSupported, ""},
		{"click with empty authorized region", func(s *eiSender) {
			s.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 100, y: 100, width: 3, height: 3, scale: 1}}}}
		}, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 10, 11, 2) }, nil, "outside"},
		{"invalid keyup identifier", func(*eiSender) {}, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "nonexistent-key") }, nil, "unknown key"},
		{"truncated UTF8 literal", func(*eiSender) {}, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), string([]byte{0xff})) }, nil, "UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			tc.disable(b.sender)
			err := tc.call(b)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("denied action error = %v; want %v", err, tc.want)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("invalid request accepted or wrong error: %v", err)
			}
			if err == nil {
				t.Fatal("unauthorized action succeeded")
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
				t.Fatalf("denied input mutated state: keys=%v buttons=%v pointer=%v", b.heldKeys, b.buttons, b.hasPointer)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("local validation revoked portal: %v", stateErr)
			}
		})
	}
}

func TestRemoteDesktopNilAndCanceledOperationsHaveNoSideEffects(t *testing.T) {
	var nilBackend *RemoteDesktopBackend
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := (&RemoteDesktopBackend{}).Close(); err != nil {
		t.Fatalf("partial backend close: %v", err)
	}
	b, peer := newWireObservedRemoteDesktop(t)
	var nilCtx context.Context
	if err := b.ScrollUp(nilCtx, 4); err == nil || !strings.Contains(err.Error(), "nil operation context") {
		t.Fatalf("nil scroll context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.ScrollRight(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scroll: %v", err)
	}
	if err := b.Type(ctx, "{enter}"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled type: %v", err)
	}
	assertNoEIWireRequest(t, peer)
}

func TestRemoteDesktopPortalOwnerLossBeforeLifecycleSubscriptionFailsClosed(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.releaseOwner = true
	portal.mu.Unlock()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/pf-test-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
	if backend != nil {
		_ = backend.Close()
		t.Fatalf("portal owner loss returned input authority: %T", backend)
	}
	if !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "subscribe session lifecycle") {
		t.Fatalf("portal owner loss error = %v, want fail-closed lifecycle subscription error", err)
	}
	portal.mu.Lock()
	selectOptions := portal.selectOptions
	portal.mu.Unlock()
	if selectOptions != nil {
		t.Fatalf("startup continued to device selection after portal owner loss: %v", selectOptions)
	}
}

func TestRemoteDesktopMouseClickButtonTransportFailureDoesNotClaimButton(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
	}}
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buttonFramePending := make(chan struct{})
	continueButtonFrame := make(chan struct{})
	var releaseFrame sync.Once
	resumeButtonFrame := func() { releaseFrame.Do(func() { close(continueButtonFrame) }) }
	defer resumeButtonFrame()
	var clockCalls atomic.Int32
	backend.sender.clockGettime = func(clockID int32, ts *unix.Timespec) error {
		if clockCalls.Add(1) == 2 {
			close(buttonFramePending)
			<-continueButtonFrame
		}
		return unix.ClockGettime(clockID, ts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- backend.MouseClick(ctx, 20, 30, 1) }()

	pointer, err := readSyntheticEISFrame(peer)
	if err != nil || pointer.object != 702 || pointer.opcode != 1 {
		t.Fatalf("click pointer movement = %+v, %v", pointer, err)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != 701 || frame.opcode != 3 {
		t.Fatalf("click pointer frame = %+v, %v", frame, err)
	}
	select {
	case <-buttonFramePending:
	case err := <-result:
		t.Fatalf("click stopped before its button frame: %v", err)
	case <-ctx.Done():
		t.Fatalf("click did not reach the button frame: %v", ctx.Err())
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	resumeButtonFrame()
	if err := <-result; !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("button-frame transport failure = %v, want revoked EIS transport error", err)
	}
	if !backend.hasPointer || backend.pointerX != 20 || backend.pointerY != 30 {
		t.Fatalf("accepted pointer movement was not recorded: %d,%d has=%v", backend.pointerX, backend.pointerY, backend.hasPointer)
	}
	if len(backend.buttons) != 0 {
		t.Fatalf("failed button frame claimed held-button ownership: %v", backend.buttons)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed button frame left authorization active: %v", stateErr)
	}
}

// An EIS transport loss during teardown is a lost authorization, not a
// successful release. Cover both the held-input release and the stop-device
// message after a prior successful session setup.
func TestRemoteDesktopReleaseHeldInputRevokesOnActualTransportLoss(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setHeld func(*RemoteDesktopBackend)
		want    string
	}{
		{name: "failed held key release", setHeld: func(b *RemoteDesktopBackend) { b.heldKeys[30] = true }, want: "EIS transport failed"},
		{name: "failed stop-device request", setHeld: func(*RemoteDesktopBackend) {}, want: "stop EIS device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			tc.setHeld(b)
			if err := b.sender.client.conn.Close(); err != nil {
				t.Fatal(err)
			}
			err := b.releaseHeldInput()
			if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("lost transport was not treated as lost authorization: %v", err)
			}
			if got := b.stateError(); !errors.Is(got, ErrRemoteDesktopRevoked) {
				t.Fatalf("transport loss left backend active: %v", got)
			}
			if tc.want == "EIS transport failed" && !b.heldKeys[30] {
				t.Fatal("failed held-key release silently discarded ownership")
			}
		})
	}
}

func TestRemoteDesktopAllAdvertisedDeviceLifecycleEventsAuthorizeRevocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     func(*RemoteDesktopBackend) uint64
		opcode uint32
	}{
		{"keyboard device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.keyboardDev }, 8},
		{"button device removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.buttonDev }, 0},
		{"text device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.textDev }, 8},
		{"scroll device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.scrollDev }, 8},
		{"keyboard interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.keyboard }, 0},
		{"button interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.button }, 0},
		{"text interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.text }, 0},
		{"scroll interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.scroll }, 0},
		{"pointer device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.pointers[0].device }, 8},
		{"pointer interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.pointers[0].pointer }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 500, pointer: 501}}
			id := tc.id(b)
			if id == 0 {
				t.Fatal("synthetic device identity unexpectedly zero")
			}
			event := eiMessage{object: id, opcode: tc.opcode}
			if !b.isEISLifecycleFailure(event) {
				t.Fatalf("device event %v not recognized as terminal", event)
			}
			client.messages <- event
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("authorized input survived device loss")
			}
			if err := b.MouseDown(context.Background(), 1); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked device admitted pointer input: %v", err)
			}
		})
	}
}

func TestRemoteDesktopUnrelatedDeviceAndUnsupportedOpcodeDoNotRevoke(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 500, pointer: 501}}
	for _, event := range []eiMessage{
		{object: 0, opcode: 0},
		{object: 9999, opcode: 0},
		{object: 9999, opcode: 8},
		{object: 501, opcode: 8},
		{object: b.sender.keyboard, opcode: 18},
		{object: b.sender.keyboardDev, opcode: 18},
		{object: b.sender.seat, opcode: 18},
	} {
		if b.isEISLifecycleFailure(event) {
			t.Fatalf("unrelated EIS frame retired authorization: %+v", event)
		}
		client.messages <- event
	}
	select {
	case <-b.revoked:
		t.Fatal("unrelated device frame revoked portal")
	case <-time.After(25 * time.Millisecond):
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("benign frames affected session: %v", err)
	}
}

func TestRemoteDesktopExtremeScrollCannotOverflowProtocolOrWrite(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"vertical max overflow", func() error { return b.ScrollDown(context.Background(), math.MaxInt) }},
		{"vertical min overflow", func() error { return b.ScrollUp(context.Background(), math.MaxInt) }},
		{"horizontal max overflow", func() error { return b.ScrollRight(context.Background(), math.MaxInt) }},
		{"horizontal min overflow", func() error { return b.ScrollLeft(context.Background(), math.MaxInt) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("out-of-range user input accepted")
			}
			if errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("invalid client input revoked authorized session: %v", err)
			}
		})
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("overflow calculation mutated authorization: %v", err)
	}
}

func TestRemoteDesktopNilCauseRevokesExactlyOnce(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	first := b.revoke(nil)
	if !errors.Is(first, ErrRemoteDesktopRevoked) {
		t.Fatalf("nil-cause revocation: %v", first)
	}
	second := b.revoke(errors.New("second failure must not replace the first"))
	if !errors.Is(second, ErrRemoteDesktopRevoked) || second.Error() != first.Error() {
		t.Fatalf("revocation was not stable: first=%v second=%v", first, second)
	}
	select {
	case <-b.revoked:
	default:
		t.Fatal("revocation did not broadcast")
	}
}

// Reject invalid portal credentials before writing anything, without
// replacing a previously persisted grant or leaving temporary credentials
// behind. A rejected token cannot silently revoke an existing permission.
func TestPortalRestoreTokenRejectedReplacementPreservesDurableGrant(t *testing.T) {
	for _, tc := range []struct{ name, token string }{
		{"empty", ""},
		{"embedded NUL", "invalid\x00token"},
		{"newline", "invalid\ntoken"},
		{"carriage return", "invalid\rtoken"},
		{"invalid UTF8", string([]byte{0xff, 0xfe})},
		{"oversized", strings.Repeat("x", 4097)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := portalTokenTestRuntime(t)
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			const previous = "previous-authorized-grant"
			if err := lock.store(previous); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if err := lock.store(tc.token); err == nil {
				t.Fatalf("invalid replacement accepted: %q", tc.name)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".restore-token-") {
					t.Fatalf("invalid replacement left staging file: %s", entry.Name())
				}
			}
			data, err := os.ReadFile(filepath.Join(dir, portalRestoreTokenFile))
			if err != nil || string(data) != previous+"\n" {
				t.Fatalf("invalid replacement altered durable grant: %q err=%v", data, err)
			}
			if got, err := lock.consume(); err != nil || got != previous {
				t.Fatalf("prior grant failed after rejected replacement: token=%q err=%v", got, err)
			}
		})
	}
}

// Maximum-length valid UTF-8 grants must round-trip without truncation,
// while a consumed credential must not be replayable by another reader.
func TestPortalRestoreTokenMaximumLengthRoundTripAndSingleUse(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	token := strings.Repeat("é", 2048)
	if len(token) != 4096 {
		t.Fatalf("test token len=%d", len(token))
	}
	if err := lock.store(token); err != nil {
		t.Fatal(err)
	}
	got, err := lock.consume()
	if err != nil || got != token {
		t.Fatalf("maximum UTF-8 token corrupted: length=%d err=%v", len(got), err)
	}
	if replay, err := lock.consume(); err != nil || replay != "" {
		t.Fatalf("consumed grant was replayable: %q err=%v", replay, err)
	}
}

func TestRemoteDesktopPortalClosedSignalRequiresSessionPathAndOwner(t *testing.T) {
	const owner = ":1.44"
	const path = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/authorized")
	backend := &RemoteDesktopBackend{portal: &portalSession{owner: owner, path: path}}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
		want   bool
	}{
		{"nil", nil, false},
		{"other interface", &dbus.Signal{Name: "org.freedesktop.portal.Request.Closed", Path: path, Sender: owner}, false},
		{"other member", &dbus.Signal{Name: portalSessionInterface + ".Opened", Path: path, Sender: owner}, false},
		{"other session", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: "/org/freedesktop/portal/desktop/session/other", Sender: owner}, false},
		{"forged owner", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: path, Sender: ":1.66"}, false},
		{"authentic owner", &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: path, Sender: owner}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backend.isAuthorizedPortalSessionClose(tc.signal); got != tc.want {
				t.Fatalf("signal %+v accepted=%v want %v", tc.signal, got, tc.want)
			}
		})
	}
}

// A successful transport receipt cannot reauthorize a command if the portal
// has already revoked the session before the receipt reaches its caller.
// Hold the synthetic writer at its acknowledgement boundary to reproduce the
// actual ordering without scheduler sleeps or a live authorized portal.
func TestRemoteDesktopSubmitDoesNotAcknowledgeReceiptAfterAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- nil
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	// revoke() publishes this state before it closes the revocation channel.
	// The synthetic seam isolates that precise interval without closing sockets.
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("revoked authorization acknowledged successful input: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}

func TestRemoteDesktopSubmitDoesNotAcknowledgeReceiptAfterBackendStopped(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- nil
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	close(b.stop)
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("stopped backend acknowledged input: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}

func TestRemoteDesktopSubmitPreservesTransportFailureWhenAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	transportErr := errors.New("synthetic transport write rejected")
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- transportErr
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) || !errors.Is(err, transportErr) {
			t.Fatalf("lost revoked or transport failure identity: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}

func TestRemoteDesktopSubmitReportsStopAfterAdmissionWithoutReceipt(t *testing.T) {
	backend := syntheticQueuedPortal()
	result := make(chan error, 1)
	ran := false
	go func() {
		result <- backend.submit(context.Background(), func() error {
			ran = true
			return nil
		})
	}()
	var command remoteDesktopCommand
	select {
	case command = <-backend.commands:
	case <-time.After(time.Second):
		t.Fatal("submit did not hand off its command")
	}
	close(backend.stop)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("stopped backend returned %v, want unavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit remained blocked after backend stop")
	}
	if ran {
		t.Fatal("stopped backend executed an unacknowledged command")
	}
	if len(command.done) != 0 {
		t.Fatalf("unobserved writer receipt queue contains %d values", len(command.done))
	}
}

// A portal may advertise the text interface object before the matching
// device is selected or resumed. A Type batch must reject that incomplete
// capability before any earlier key, modifier, or pointer-independent text
// events are injected into another application.
func TestRemoteDesktopTypeDoesNotPartiallyInjectWhenTextDeviceMissing(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		disable        func(*eiSender)
	}{
		{"key prefix then missing text device", "{enter}private", func(s *eiSender) { s.textDev = 0 }},
		{"modifier prefix then missing text device", "{ctrl+a}private", func(s *eiSender) { s.textDev = 0 }},
		{"key prefix then missing text object", "{enter}private", func(s *eiSender) { s.text = 0 }},
		{"pure literal missing text device", "private", func(s *eiSender) { s.textDev = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			tc.disable(b.sender)
			if err := b.Type(context.Background(), tc.sequence); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("incomplete text authority accepted or wrong error: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("rejected batch changed held keys: %v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation revoked otherwise live input session: %v", err)
			}
		})
	}
}

// The gate follows API and parser preflight, writer admission, and the first
// action in a two-action Type batch.
const portalTypeSecondActionErrCall = 11

type portalTypeActionContextGate struct {
	context.Context
	calls   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

func (c *portalTypeActionContextGate) Err() error {
	if c.calls.Add(1) == portalTypeSecondActionErrCall {
		close(c.reached)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}

func TestRemoteDesktopTypeStopsBeforeNextActionAfterCancellation(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	baseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx := &portalTypeActionContextGate{
		Context: baseCtx,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() { result <- backend.Type(ctx, "a{enter}") }()
	select {
	case <-ctx.reached:
	case <-baseCtx.Done():
		t.Fatalf("Type did not reach the next action boundary: %v", baseCtx.Err())
	}
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	textFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read first text frame: %v", err)
	}
	if textFrame.object != backend.sender.text || textFrame.opcode != 2 {
		t.Fatalf("first action frame = object %d opcode %d, want text object %d opcode 2", textFrame.object, textFrame.opcode, backend.sender.text)
	}
	text, err := eiReadString(textFrame.body, 0)
	if err != nil || text != "a" {
		t.Fatalf("first action text = %q, %v; want %q", text, err, "a")
	}
	deviceFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read first text device frame: %v", err)
	}
	if deviceFrame.object != backend.sender.textDev || deviceFrame.opcode != 3 || len(deviceFrame.body) != 12 {
		t.Fatalf("first text device frame = %+v", deviceFrame)
	}
	if err := peer.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	cancel()
	close(ctx.release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Type returned %v after cancellation, want context.Canceled", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close writer after canceled batch: %v", err)
	}
	stopFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read EIS device stop frame: %v", err)
	}
	if stopFrame.object != backend.sender.keyboardDev || stopFrame.opcode != 2 || len(stopFrame.body) != 4 {
		t.Fatalf("frame after canceled Type batch = %+v, want device stop request", stopFrame)
	}
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var unexpected [1]byte
	if n, err := peer.Read(unexpected[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("later Type action reached EIS after cancellation: bytes=%x err=%v", unexpected[:n], err)
	}
}

// An EIS key-down belongs to one caller-held key. A second key-down for an
// already-owned key, whether in the same Type batch or from KeyDown, must
// never re-inject a press (or leak earlier typed content from that batch).
func TestRemoteDesktopTypeRejectsRepeatedKeyDownBeforeEmittingAnyBatchFrames(t *testing.T) {
	for _, tc := range []struct{ name, key, batch string }{
		{name: "duplicate within batch", batch: "private{a down}{a down}"},
		{name: "duplicate modifier within batch", batch: "{ctrl down}{ctrl down}"},
		{name: "duplicate previously held key", key: "a", batch: "private{a down}"},
		{name: "duplicate previously held modifier", key: "ctrl", batch: "{a}{ctrl down}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if tc.key != "" {
				if err := b.KeyDown(context.Background(), tc.key); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := readSyntheticEISFrame(peer); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := len(b.heldKeys)
			err := b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate EIS press accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != before {
				t.Fatalf("denied batch mutated held state: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("preflight revoked EIS session: %v", err)
			}
		})
	}
}

func TestRemoteDesktopTypeRejectsEmbeddedNULBeforeEarlierKeyFrames(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	err := backend.Type(context.Background(), "{enter}prefix\x00suffix")
	if err == nil || !strings.Contains(err.Error(), "embedded NUL") {
		t.Fatalf("Type with embedded NUL = %v, want text validation error", err)
	}
	assertNoEIWireRequest(t, peer)
	if len(backend.heldKeys) != 0 {
		t.Fatalf("rejected Type batch changed held keys: %v", backend.heldKeys)
	}
	if stateErr := backend.stateError(); stateErr != nil {
		t.Fatalf("local text validation revoked authorization: %v", stateErr)
	}
}

// A Type tap ends with key-up. If another operation is holding that key,
// accepting a tap would silently release the caller's drag/shortcut modifier.
// Reject the *entire* Type batch before any earlier text or key event escapes.
func TestRemoteDesktopTypeNeverTapsCallerHeldKey(t *testing.T) {
	for _, tc := range []struct{ name, key, batch string }{
		{"letter", "a", "{a}"},
		{"enter with prefix", "enter", "private{enter}"},
		{"modifier", "ctrl", "{ctrl}"},
		{"modified key", "a", "{ctrl+a}"},
		{"late tap after named key", "a", "{enter}{a}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if err := b.KeyDown(context.Background(), tc.key); err != nil {
				t.Fatalf("establish held key: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("held key frame: %v", err)
				}
			}
			code, err := remoteKeyCode(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if !b.heldKeys[code] {
				t.Fatal("held key not recorded")
			}
			err = b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("held key tapped/released by Type: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if !b.heldKeys[code] || len(b.heldKeys) != 1 {
				t.Fatalf("denied batch changed held input: %+v", b.heldKeys)
			}
			if err := b.KeyUp(context.Background(), tc.key); err != nil {
				t.Fatalf("caller cannot release held key: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("legitimate release not sent: %v", err)
				}
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("legitimate release did not clear state: %+v", b.heldKeys)
			}
		})
	}
}

// Preflight must simulate ownership within the batch as well as across calls.
// The later invalid tap must not leave an earlier down injected or recorded.
func TestRemoteDesktopTypePreflightRejectsTapAfterDownWithinSameBatch(t *testing.T) {
	for _, sequence := range []string{
		"{a down}{a}",
		"private{enter down}{enter}",
		"{ctrl down}{ctrl+a}{ctrl}",
	} {
		t.Run(sequence, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), sequence)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("invalid self-releasing batch accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("batch injected held keys despite rejection: %+v", b.heldKeys)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("batch rejected by revoking EIS authorization: %v", stateErr)
			}
		})
	}
}

// Balanced explicit ownership and independent key presses are still allowed.
func TestRemoteDesktopTypeBalancedSameKeyTransactionsStillWork(t *testing.T) {
	for _, sequence := range []string{"{a down}{a up}{a}", "{ctrl down}{ctrl+a}{ctrl up}", "{enter}{enter}"} {
		t.Run(sequence, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if err := b.Type(context.Background(), sequence); err != nil {
				t.Fatalf("balanced EIS sequence rejected: %v", err)
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("balanced sequence left key held: %+v", b.heldKeys)
			}
			if frame, err := readSyntheticEISFrame(peer); err != nil || frame.object != b.sender.keyboard {
				t.Fatalf("normal key transitions absent: %+v err=%v", frame, err)
			}
		})
	}
}

func TestRemoteDesktopTypeTextTransportFailureRevokesAuthorization(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.Type(context.Background(), "literal text")
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("Type text transport failure = %v, want revoked EIS transport error", err)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed text Type left authorization active: %v", stateErr)
	}
}

func TestRemoteDesktopTypeTransportFailureDoesNotRecordKeyOwnership(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.Type(context.Background(), "{a}")
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("Type transport failure = %v, want revoked EIS transport error", err)
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("failed key frame recorded held-key ownership: %v", backend.heldKeys)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed Type left authorization active: %v", stateErr)
	}
}

// A Type batch must not release an ordinary key that this EIS writer never
// pressed, even after a safe earlier text or key action in the same batch.
func TestRemoteDesktopTypeRejectsUnownedOrdinaryKeyReleaseBeforeAnyWireFrames(t *testing.T) {
	for _, tc := range []struct{ name, batch string }{
		{"unheld letter", "{a up}"},
		{"unheld enter after private text", "private{enter up}"},
		{"unheld function key after valid tap", "{b}{f5 up}"},
		{"double release in same batch", "{a down}{a up}{a up}"},
		{"release without down following text", "confidential{space up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unowned release in %q accepted: %v", tc.batch, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("refused batch retained held keys: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("invalid batch revoked valid session: %v", err)
			}
		})
	}
}

// An EIS serial is an inbound server value, not a count of outbound requests.
// Observe the real private Unix socket to prove a rejected command sent no bytes.
func newWireObservedRemoteDesktop(t *testing.T) (*RemoteDesktopBackend, *net.UnixConn) {
	t.Helper()
	connection, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = peer.Close() })
	client := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	device := eiServerIDFloor + 0x21
	sender := &eiSender{client: client, connection: eiServerIDFloor + 0x10, seat: eiServerIDFloor + 0x11,
		keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: device, buttonDev: device, scrollDev: device, textDev: device}
	b := newRemoteDesktopBackend(sender, &portalSession{})
	t.Cleanup(func() { _ = b.Close() })
	return b, peer
}

func assertNoEIWireRequest(t *testing.T, peer *net.UnixConn) {
	t.Helper()
	if err := peer.SetReadDeadline(time.Now().Add(15 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var value [1]byte
	n, err := peer.Read(value[:])
	if err := peer.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n != 0 || !os.IsTimeout(err) {
		t.Fatalf("rejected command emitted %d EIS bytes (first=%x): %v", n, value, err)
	}
}

// Every Type command is a logical batch: an unheld modifier release at the
// tail must fail before the earlier text or accelerator is dispatched. The
// check must run on the serialized EIS writer, which owns heldKeys.
func TestRemoteDesktopRejectsUnheldModifierReleaseBeforeBatchDispatch(t *testing.T) {
	for _, tc := range []struct{ name, sequence string }{
		{"stray ctrl", "{ctrl up}"},
		{"literal prefix", "private{ctrl up}"},
		{"named prefix", "{enter}{shift up}"},
		{"accelerator prefix", "{ctrl+a}{alt up}"},
		{"different held modifier", "{ctrl down}{shift up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), tc.sequence)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unauthorized release accepted: err=%v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("invalid command changed held keys: %v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation error revoked authorized portal: %v", err)
			}
		})
	}
}

func TestRemoteDesktopAcceptsBalancedAndCallerHeldModifierRelease(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	if err := b.Type(context.Background(), "{ctrl down}{ctrl up}"); err != nil {
		t.Fatalf("balanced modifier sequence rejected: %v", err)
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("balanced sequence stranded keys: %v", b.heldKeys)
	}
	if err := b.KeyDown(context.Background(), "shift"); err != nil {
		t.Fatal(err)
	}
	if err := b.Type(context.Background(), "{shift up}"); err != nil {
		t.Fatalf("caller-owned modifier release rejected: %v", err)
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("caller-owned modifier not released: %v", b.heldKeys)
	}
}

func TestRemoteDesktopKeyUpRejectsUnheldModifierWithoutEISWireWrites(t *testing.T) {
	for _, name := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unowned modifier release accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("rejected modifier release changed held keys")
			}
			if err := b.KeyDown(context.Background(), name); err != nil {
				t.Fatalf("valid down failed: %v", err)
			}
			if err := b.KeyUp(context.Background(), name); err != nil {
				t.Fatalf("valid up failed: %v", err)
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("legitimate release retained held state: %v", b.heldKeys)
			}
			for i, down := range []bool{true, false} {
				key, err := readSyntheticEISFrame(peer)
				if err != nil || key.object != b.sender.keyboard || key.opcode != 1 || len(key.body) < 8 || (binary.NativeEndian.Uint32(key.body[4:8]) == 1) != down {
					t.Fatalf("valid key event %d not transmitted: %+v %v", i, key, err)
				}
				if frame, err := readSyntheticEISFrame(peer); err != nil || frame.object != b.sender.keyboardDev || frame.opcode != 3 {
					t.Fatalf("synchronized EIS key frame %d absent: %+v %v", i, frame, err)
				}
			}
		})
	}
}

// An unheld button-up can end an independently held drag in the target
// compositor. The EIS writer must reject it before sending any wire bytes.
func TestRemoteDesktopNeverReleasesUnheldButtonOnEISWire(t *testing.T) {
	for _, button := range []int{1, 2, 3} {
		t.Run([]string{"left", "middle", "right"}[button-1], func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.MouseUp(context.Background(), button)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld button %d release accepted: %v", button, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.buttons) != 0 {
				t.Fatalf("invalid release changed held button set: %v", b.buttons)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("local validation revoked session: %v", err)
			}
			if err := b.MouseDown(context.Background(), button); err != nil {
				t.Fatalf("valid down rejected: %v", err)
			}
			if err := b.MouseUp(context.Background(), button); err != nil {
				t.Fatalf("valid up rejected: %v", err)
			}
			for i := 0; i < 4; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("valid button transition %d absent: %v", i, err)
				}
			}
			if len(b.buttons) != 0 {
				t.Fatalf("valid up retained button state: %v", b.buttons)
			}
		})
	}
}

// A direct KeyUp only releases input held by this serialized EIS writer. An
// unsolicited regular-key release must not affect key state owned by a different
// operation or another client, just as an unheld modifier release is rejected.
func TestRemoteDesktopKeyUpRejectsUnownedOrdinaryKeysWithoutWireWrites(t *testing.T) {
	for _, key := range []string{"a", "enter", "f5", "space"} {
		t.Run(key, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.KeyUp(context.Background(), key)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld key %q was released: %v", key, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("unheld key mutated ownership: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation unexpectedly revoked session: %v", err)
			}
			if err := b.KeyDown(context.Background(), key); err != nil {
				t.Fatalf("valid key down rejected: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("down frame %d: %v", i, err)
				}
			}
			if err := b.KeyUp(context.Background(), key); err != nil {
				t.Fatalf("owned key release refused: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("up frame %d: %v", i, err)
				}
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("owned release retained state: %+v", b.heldKeys)
			}
		})
	}
}

// A non-ASCII character may case-fold to an ASCII letter. A physical key
// operation must not silently transliterate that character into another key.
func TestPortalRemoteKeyCodeRejectsNonASCIICasefoldAliases(t *testing.T) {
	for _, name := range []string{"İ", "K", "𝑨"} { // Non-ASCII letters cannot authorize a different ASCII key
		if code, err := remoteKeyCode(name); err == nil || code != 0 || !strings.Contains(err.Error(), "unknown key") {
			t.Fatalf("Unicode alias %q mapped to physical code %d: %v", name, code, err)
		}
		b, _, _ := newSyntheticRevocableRemoteDesktop(t)
		if err := b.KeyDown(context.Background(), name); err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Fatalf("invalid requested Unicode key %q pressed device: %v", name, err)
		}
		if len(b.heldKeys) != 0 {
			t.Fatalf("invalid key acquired physical key ownership: %+v", b.heldKeys)
		}
	}
	for _, key := range []string{"a", "A", "ctrl", "f12", "/", "?"} {
		if _, err := remoteKeyCode(key); err != nil {
			t.Fatalf("valid ASCII physical key %q rejected: %v", key, err)
		}
	}
}

// The writer is a second authorization and cancellation boundary. An event
// already queued before the caller is canceled or the portal is retired
// must never execute even when the channel delivers it successfully.
func TestRemoteDesktopWriterRejectsQueuedCommandsWithoutExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		revoke bool
		want   error
	}{
		{name: "canceled before dequeue", cancel: true, want: context.Canceled},
		{name: "authorization revoked before dequeue", revoke: true, want: ErrRemoteDesktopRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			b.commands = make(chan remoteDesktopCommand, 1)
			b.writerDone = make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ran := false
			command := remoteDesktopCommand{ctx: ctx, done: make(chan error, 1), run: func() error { ran = true; return nil }}
			b.commands <- command
			if tc.cancel {
				cancel()
			}
			if tc.revoke {
				b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			}
			go b.writeLoop()
			select {
			case err := <-command.done:
				if !errors.Is(err, tc.want) {
					t.Fatalf("queued command returned %v, want %v", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("queued writer command did not receive receipt")
			}
			if ran {
				t.Fatal("unauthorized queued command executed")
			}
			close(b.stop)
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("writer did not retire")
			}
		})
	}
}
