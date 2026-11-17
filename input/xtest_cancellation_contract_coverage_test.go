//go:build linux

package input

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestXTestTypeStopsDuringPreflightBeforeAnyWireInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []xtestEvent
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 9}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			cancel()
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: 1,
				Keysyms:           []xproto.Keysym{'a', 0xffe3},
			})
		},
		FakeInputCheckedFunc: func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
			events = append(events, xtestEvent{eventType: eventType, detail: detail})
			return x11.NewMockXTestFakeInputCookie(nil)
		},
	}
	backend := &XTestBackend{conn: conn, root: 1}

	err := backend.Type(ctx, "a{ctrl}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type error = %v, want cancellation during batch preflight", err)
	}
	if len(events) != 0 {
		t.Fatalf("canceled preflight emitted input events: %+v", events)
	}
}

func TestXTestTypeStopsBeforeNextActionAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a', 0xffe3}, func(event xtestEvent) error {
		if event == (xtestEvent{xproto.KeyRelease, 8}) {
			cancel()
		}
		return nil
	})

	err := backend.Type(ctx, "a{ctrl}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type error = %v, want cancellation before the next action", err)
	}
	want := []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyRelease, 8}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("Type events = %+v, want only the completed first key action %+v", *events, want)
	}
}

func TestXTestTypeLiteralStopsBetweenRunesAfterBalancedKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a', 'b'}, func(event xtestEvent) error {
		if event == (xtestEvent{xproto.KeyRelease, 8}) {
			cancel()
		}
		return nil
	})

	err := backend.TypeLiteral(ctx, "ab")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TypeLiteral error = %v, want cancellation between runes", err)
	}
	want := []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyRelease, 8}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("TypeLiteral events = %+v, want only a balanced first rune %+v", *events, want)
	}
}

func TestXTestTypedModifierReleaseFailureRetainsHeldOwnership(t *testing.T) {
	releaseErr := errors.New("synthetic XTEST release failure")
	failRelease := false
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3}, func(event xtestEvent) error {
		if failRelease && event == (xtestEvent{xproto.KeyRelease, 8}) {
			return releaseErr
		}
		return nil
	})
	if err := backend.Type(context.Background(), "{ctrl down}"); err != nil {
		t.Fatalf("hold Ctrl: %v", err)
	}
	*events = nil
	failRelease = true
	err := backend.Type(context.Background(), "{ctrl up}")
	if !errors.Is(err, releaseErr) {
		t.Fatalf("typed release error = %v, want %v", err, releaseErr)
	}
	if !reflect.DeepEqual(*events, []xtestEvent{{xproto.KeyRelease, 8}}) {
		t.Fatalf("typed release events = %+v, want one failed release", *events)
	}
	if !backend.modifierHeld(8) {
		t.Fatal("failed typed release cleared caller-owned modifier state")
	}
}

func TestXTestMouseClickDoesNotClickWhenPointerMoveFails(t *testing.T) {
	moveErr := errors.New("synthetic pointer movement failure")
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, func(event xtestEvent) error {
		if event.eventType == xproto.MotionNotify {
			return moveErr
		}
		return nil
	})

	err := backend.MouseClick(context.Background(), 20, 30, 1)
	if !errors.Is(err, moveErr) {
		t.Fatalf("MouseClick error = %v, want movement failure %v", err, moveErr)
	}
	want := []xtestEvent{{eventType: xproto.MotionNotify}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("MouseClick events = %+v, want movement only %+v", *events, want)
	}
}
