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

func TestTypeCtrlA(t *testing.T) {
	// Map ctrl (0xffe3=65507) and 'a' (0x61=97) each to separate keycodes.
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
	b.delay = 0

	var events []byte
	mc.FakeInputCheckedFunc = func(eventType, _ byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	// Type("{ctrl+a}") should press Ctrl, tap A, release Ctrl = 4 events
	if err := b.TypeContext(context.Background(), "{ctrl+a}"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}
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

func TestTypeContextUppercaseUsesShift(t *testing.T) {
	// Setup: MinKeycode=8, MaxKeycode=12 (5 keycodes), 2 keysyms per keycode.
	// Level 0: lowercase, Level 1: uppercase (standard US QWERTY layout).
	// Keymap per keycode: [level0, level1]
	//   keycode 8: [h(0x68), H(0x48)]
	//   keycode 9: [e(0x65), E(0x45)]
	//   keycode 10: [l(0x6c), L(0x4c)]
	//   keycode 11: [o(0x6f), O(0x4f)]
	//   keycode 12: [NoSymbol, Shift_L(0xffe1)]
	//
	// typeText now looks up each character's keysym directly:
	//   'H' → keysym 0x48 at keycode 8, level 1 → needs Shift
	//   'e' → keysym 0x65 at keycode 9, level 0 → no Shift
	//   'l' → keysym 0x6c at keycode 10, level 0 → no Shift
	//   'l' → same
	//   'o' → keysym 0x6f at keycode 11, level 0 → no Shift
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 12} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 2,
			Keysyms: []xproto.Keysym{
				0x68, 0x48, // keycode 8: h, H
				0x65, 0x45, // keycode 9: e, E
				0x6c, 0x4c, // keycode 10: l, L
				0x6f, 0x4f, // keycode 11: o, O
				0x0, 0xffe1, // keycode 12: NoSymbol, Shift_L
			},
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	b.delay = 0

	type fakeInputEvent struct {
		eventType byte
		detail    byte
	}
	var events []fakeInputEvent
	mc.FakeInputCheckedFunc = func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, fakeInputEvent{eventType, detail})
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	if err := b.TypeContext(context.Background(), "Hello"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	// Expected: H(Shift down/up) + e + l + l + o = 12 events
	hKC := byte(8)
	eKC := byte(9)
	lKC := byte(10)
	oKC := byte(11)
	shiftKC := byte(12)

	if len(events) != 12 {
		t.Fatalf("expected 12 events for 'Hello', got %d: %v", len(events), events)
	}

	expected := []fakeInputEvent{
		{xproto.KeyPress, shiftKC},   // Shift down (H is at level 1)
		{xproto.KeyPress, hKC},       // H press
		{xproto.KeyRelease, hKC},     // H release
		{xproto.KeyRelease, shiftKC}, // Shift up
		{xproto.KeyPress, eKC},       // e press (level 0, no Shift)
		{xproto.KeyRelease, eKC},     // e release
		{xproto.KeyPress, lKC},       // l press
		{xproto.KeyRelease, lKC},     // l release
		{xproto.KeyPress, lKC},       // l press
		{xproto.KeyRelease, lKC},     // l release
		{xproto.KeyPress, oKC},       // o press
		{xproto.KeyRelease, oKC},     // o release
	}

	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(events), events)
	}

	for i, exp := range expected {
		got := events[i]
		if got.eventType != exp.eventType || got.detail != exp.detail {
			t.Errorf("event %d: got (type=%d, detail=%d), want (type=%d, detail=%d)",
				i, got.eventType, got.detail, exp.eventType, exp.detail)
		}
	}
}
