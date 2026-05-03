//go:build linux
// +build linux

package input

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func newXTestMock(t *testing.T, keysyms []xproto.Keysym) (*XTestBackend, *x11.MockConnection) {
	t.Helper()
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           keysyms,
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	return b, mc
}

func TestKeyDownMapsToFakeInput(t *testing.T) {
	b, mc := newXTestMock(t, []xproto.Keysym{0x61}) // 'a'
	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("KeyDown: %v", err)
	}
	if mc.LastFakeInput.EventType != xproto.KeyPress {
		t.Fatalf("expected KeyPress, got %d", mc.LastFakeInput.EventType)
	}
}

func TestKeyUpMapsToKeyRelease(t *testing.T) {
	b, mc := newXTestMock(t, []xproto.Keysym{0x61})
	if err := b.KeyUp(context.Background(), "a"); err != nil {
		t.Fatalf("KeyUp: %v", err)
	}
	if mc.LastFakeInput.EventType != xproto.KeyRelease {
		t.Fatalf("expected KeyRelease, got %d", mc.LastFakeInput.EventType)
	}
}

func TestKeyUnknownReturnsError(t *testing.T) {
	b, _ := newXTestMock(t, []xproto.Keysym{})
	err := b.KeyDown(context.Background(), "boguskey")
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestMouseMoveUsesMotionNotify(t *testing.T) {
	b, mc := newXTestMock(t, nil)
	if err := b.MouseMove(context.Background(), 100, 200); err != nil {
		t.Fatalf("MouseMove: %v", err)
	}
	if mc.LastFakeInput.EventType != xproto.MotionNotify {
		t.Fatalf("expected MotionNotify, got %d", mc.LastFakeInput.EventType)
	}
	if mc.LastFakeInput.X != 100 || mc.LastFakeInput.Y != 200 {
		t.Fatalf("expected (100,200), got (%d,%d)", mc.LastFakeInput.X, mc.LastFakeInput.Y)
	}
}

func TestPressComboSendsAllKeys(t *testing.T) {
	// Map ctrl (0xffe3=65507) and 'a' (0x61=97) each to separate keycodes.
	// Setup: MinKeycode=8, MaxKeycode=9, so count=2; provide 2 keysyms.
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 9} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0xffe3, 0x61}, // keycode 8→ctrl, 9→a
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	b.delay = 0 // no sleep in tests

	var events []byte
	mc.FakeInputCheckedFunc = func(eventType, _ byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	if err := b.PressCombo(context.Background(), "ctrl+a"); err != nil {
		t.Fatalf("PressCombo: %v", err)
	}
	// 2 KeyPress + 2 KeyRelease
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %v", len(events), events)
	}
	if events[0] != xproto.KeyPress || events[1] != xproto.KeyPress {
		t.Errorf("first two events should be KeyPress: %v", events[:2])
	}
	if events[2] != xproto.KeyRelease || events[3] != xproto.KeyRelease {
		t.Errorf("last two events should be KeyRelease: %v", events[2:])
	}
}

func TestKeyTapUppercaseSendsShift(t *testing.T) {
	// Setup: MinKeycode=8, MaxKeycode=9 (2 keycodes)
	// keycode 8→'i' (0x69), keycode 9→shift (0xffe1)
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 9} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0x69, 0xffe1}, // keycode 8→'i', 9→shift
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	b.delay = 0 // no sleep in tests

	var events []byte
	var eventKeys []xproto.Keycode
	mc.FakeInputCheckedFunc = func(eventType, keycode byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		eventKeys = append(eventKeys, xproto.Keycode(keycode))
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	// KeyTap("I") should send Shift + 'i'
	if err := b.KeyTap(context.Background(), "I"); err != nil {
		t.Fatalf("KeyTap: %v", err)
	}
	// Expected: Shift down, 'i' down, 'i' up, Shift up = 4 events
	if len(events) != 4 {
		t.Fatalf("expected 4 events for uppercase KeyTap, got %d: %v", len(events), events)
	}
	// First event should be Shift press (keycode 9), then 'i' press (keycode 8)
	// Pattern: KeyPress(9), KeyPress(8), KeyRelease(8), KeyRelease(9)
	if events[0] != xproto.KeyPress || events[1] != xproto.KeyPress ||
		events[2] != xproto.KeyRelease || events[3] != xproto.KeyRelease {
		t.Errorf("wrong event sequence: %v", events)
	}
	// Check keycodes: shift=9, i=8
	if eventKeys[0] != 9 || eventKeys[1] != 8 || eventKeys[2] != 8 || eventKeys[3] != 9 {
		t.Errorf("wrong keycode sequence: %v (expected shift=9, i=8)", eventKeys)
	}
}

func TestTypeContextUppercaseSendsShift(t *testing.T) {
	// Setup: MinKeycode=8, MaxKeycode=12 (5 keycodes)
	// Need to map: h(0x68), e(0x65), l(0x6c), o(0x6f), shift(0xffe1)
	// We'll map them sequentially: 8→h, 9→e, 10→l, 11→o, 12→shift
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 12} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms: []xproto.Keysym{
				0x68,   // keycode 8→'h'
				0x65,   // keycode 9→'e'
				0x6c,   // keycode 10→'l'
				0x6f,   // keycode 11→'o'
				0xffe1, // keycode 12→shift
			},
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	b.delay = 0 // no sleep in tests

	var events []byte
	mc.FakeInputCheckedFunc = func(eventType, _ byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	// Type "Hello" - 'H' is uppercase
	if err := b.TypeContext(context.Background(), "Hello"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}
	// Expected events for "Hello":
	// Shift down, 'h' down, 'h' up, Shift up, 'e' down, 'e' up, 'l' down, 'l' up, 'l' down, 'l' up, 'o' down, 'o' up
	// = 12 events
	if len(events) != 12 {
		t.Fatalf("expected 12 events for 'Hello', got %d: %v", len(events), events)
	}
	// Check that we have the right pattern of KeyPress/KeyRelease
	pressCount := 0
	releaseCount := 0
	for _, e := range events {
		if e == xproto.KeyPress {
			pressCount++
		} else if e == xproto.KeyRelease {
			releaseCount++
		}
	}
	if pressCount != 6 || releaseCount != 6 {
		t.Errorf("expected 6 KeyPress and 6 KeyRelease, got %d/%d", pressCount, releaseCount)
	}
}
