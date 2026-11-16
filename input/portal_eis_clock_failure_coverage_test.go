//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
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
