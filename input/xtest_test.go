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

func TestTypeContextUppercaseDirectKeysym(t *testing.T) {
	// Setup: MinKeycode=8, MaxKeycode=12 (5 keycodes)
	// Keysym layout: H(0x48), e(0x65), l(0x6c), o(0x6f), shift(0xffe1)
	// Note: 'H' is 0x48, not 0x68 — typeText sends each character's own
	// keysym directly instead of decomposing uppercase into Shift+lowercase.
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 12} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms: []xproto.Keysym{
				0x48,   // keycode 8→'H' (uppercase keysym, not 'h')
				0x65,   // keycode 9→'e'
				0x6c,   // keycode 10→'l'
				0x6f,   // keycode 11→'o'
				0xffe1, // keycode 12→shift (unused in direct mode)
			},
		})
	}
	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	b.delay = 0

	// Capture full event history: each entry is (eventType, detail/keycode).
	type fakeInputEvent struct {
		eventType byte
		detail    byte
	}
	var events []fakeInputEvent
	mc.FakeInputCheckedFunc = func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, fakeInputEvent{eventType, detail})
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	// Type "Hello" — 'H' sends keysym 0x48 directly, no Shift needed.
	if err := b.TypeContext(context.Background(), "Hello"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	// Expected: each character is a press+release pair = 10 events total.
	// No Shift events at all — typeText sends keysyms directly.
	hKC := byte(8)
	eKC := byte(9)
	lKC := byte(10)
	oKC := byte(11)

	if len(events) != 10 {
		t.Fatalf("expected 10 events for 'Hello' (5 chars × press+release), got %d: %v", len(events), events)
	}

	expected := []fakeInputEvent{
		{xproto.KeyPress, hKC},   // H press
		{xproto.KeyRelease, hKC}, // H release
		{xproto.KeyPress, eKC},   // e press
		{xproto.KeyRelease, eKC}, // e release
		{xproto.KeyPress, lKC},   // l press
		{xproto.KeyRelease, lKC}, // l release
		{xproto.KeyPress, lKC},   // l press
		{xproto.KeyRelease, lKC}, // l release
		{xproto.KeyPress, oKC},   // o press
		{xproto.KeyRelease, oKC}, // o release
	}

	for i, exp := range expected {
		got := events[i]
		if got.eventType != exp.eventType || got.detail != exp.detail {
			t.Errorf("event %d: got (type=%d, detail=%d), want (type=%d, detail=%d)",
				i, got.eventType, got.detail, exp.eventType, exp.detail)
		}
	}
}


