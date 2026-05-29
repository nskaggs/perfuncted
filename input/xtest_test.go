//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"

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

type closeSpyX11Connection struct {
	x11.MockConnection
	closed bool
	synced bool
}

func (c *closeSpyX11Connection) Close() {
	c.closed = true
}

func (c *closeSpyX11Connection) Sync() {
	c.synced = true
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

func TestMouseClickUsesMotionPressRelease(t *testing.T) {
	b, mc := newXTestMock(t, nil)
	b.delay = 0

	var events []struct {
		eventType byte
		detail    byte
	}
	mc.FakeInputCheckedFunc = func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, struct {
			eventType byte
			detail    byte
		}{eventType: eventType, detail: detail})
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	if err := b.MouseClick(context.Background(), 11, 22, 1); err != nil {
		t.Fatalf("MouseClick: %v", err)
	}

	want := []struct {
		eventType byte
		detail    byte
	}{
		{eventType: xproto.MotionNotify, detail: 0},
		{eventType: xproto.ButtonPress, detail: 1},
		{eventType: xproto.ButtonRelease, detail: 1},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i, exp := range want {
		if events[i] != exp {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], exp)
		}
	}
}

func TestScrollMethodsUseExpectedButtons(t *testing.T) {
	b, mc := newXTestMock(t, nil)
	var events []struct {
		eventType byte
		detail    byte
	}
	mc.FakeInputCheckedFunc = func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, struct {
			eventType byte
			detail    byte
		}{eventType: eventType, detail: detail})
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	tests := []struct {
		name string
		run  func() error
		want byte
	}{
		{name: "ScrollUp", run: func() error { return b.ScrollUp(context.Background(), 1) }, want: 4},
		{name: "ScrollDown", run: func() error { return b.ScrollDown(context.Background(), 1) }, want: 5},
		{name: "ScrollLeft", run: func() error { return b.ScrollLeft(context.Background(), 1) }, want: 6},
		{name: "ScrollRight", run: func() error { return b.ScrollRight(context.Background(), 1) }, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events = events[:0]
			if err := tt.run(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			want := []struct {
				eventType byte
				detail    byte
			}{
				{eventType: xproto.ButtonPress, detail: tt.want},
				{eventType: xproto.ButtonRelease, detail: tt.want},
			}
			if len(events) != len(want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			for i, exp := range want {
				if events[i] != exp {
					t.Fatalf("event %d = %+v, want %+v", i, events[i], exp)
				}
			}
		})
	}
}

func TestPointerLocationReportsCurrentPosition(t *testing.T) {
	b, mc := newXTestMock(t, nil)
	mc.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
		return x11.NewMockQueryPointerCookie(&xproto.QueryPointerReply{
			RootX: 321,
			RootY: 654,
		})
	}

	x, y, err := b.PointerLocation(context.Background())
	if err != nil {
		t.Fatalf("PointerLocation: %v", err)
	}
	if x != 321 || y != 654 {
		t.Fatalf("PointerLocation = (%d,%d), want (321,654)", x, y)
	}
}

func TestXTestBackend_CloseInvokesConnectionClose(t *testing.T) {
	spy := &closeSpyX11Connection{}
	spy.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	spy.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8} }
	spy.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0x61},
		})
	}

	b, err := NewXTestBackendWithConn(spy)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !spy.closed {
		t.Fatal("Close did not close underlying connection")
	}
}

func TestXTestBackend_SyncInvokesConnectionSync(t *testing.T) {
	spy := &closeSpyX11Connection{}
	spy.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	spy.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8} }
	spy.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0x61},
		})
	}

	b, err := NewXTestBackendWithConn(spy)
	if err != nil {
		t.Fatalf("NewXTestBackendWithConn: %v", err)
	}
	if err := b.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !spy.synced {
		t.Fatal("Sync did not call underlying connection Sync")
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
	if err := b.Type(context.Background(), "{ctrl+a}"); err != nil {
		t.Fatalf("Type: %v", err)
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

func TestTypeExplicitKeyUpReleasesHeldKey(t *testing.T) {
	b, mc := newXTestMock(t, []xproto.Keysym{0x61})
	b.delay = 0

	var events []byte
	mc.FakeInputCheckedFunc = func(eventType, _ byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	if err := b.TypeContext(context.Background(), "{a down}{a up}"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []byte{xproto.KeyPress, xproto.KeyRelease}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(events), events)
	}
	for i, exp := range want {
		if events[i] != exp {
			t.Fatalf("event %d = %d, want %d (all events: %v)", i, events[i], exp, events)
		}
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

func TestMouseClickCancelsAndReleases(t *testing.T) {
	b, mc := newXTestMock(t, []xproto.Keysym{0x61})
	b.delay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	var events []byte
	mc.FakeInputCheckedFunc = func(eventType, detail byte, tm uint32, window xproto.Window, x, y int16, device byte) x11.XTestFakeInputCookie {
		events = append(events, eventType)
		if eventType == xproto.ButtonPress {
			cancel()
		}
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	err := b.MouseClick(ctx, 100, 200, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MouseClick returned %v, want context.Canceled", err)
	}
	if len(events) < 3 || events[len(events)-1] != xproto.ButtonRelease {
		t.Fatalf("events = %v, want release cleanup", events)
	}
}

func TestTypeContextCachesKeyboardMapping(t *testing.T) {
	b, mc := newXTestMock(t, []xproto.Keysym{0x61})
	b.delay = 0

	var mappingCalls int
	mc.GetKeyboardMappingFunc = func(first xproto.Keycode, count byte) x11.GetKeyboardMappingCookie {
		mappingCalls++
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0x61},
		})
	}

	if err := b.TypeContext(context.Background(), "a"); err != nil {
		t.Fatalf("TypeContext #1: %v", err)
	}
	if err := b.TypeContext(context.Background(), "a"); err != nil {
		t.Fatalf("TypeContext #2: %v", err)
	}
	if mappingCalls != 1 {
		t.Fatalf("GetKeyboardMapping calls = %d, want 1", mappingCalls)
	}
}

func BenchmarkTypeContextCachedKeymap(b *testing.B) {
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8} }
	mc.GetKeyboardMappingFunc = func(_ xproto.Keycode, _ byte) x11.GetKeyboardMappingCookie {
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
			KeysymsPerKeycode: 1,
			Keysyms:           []xproto.Keysym{0x61},
		})
	}
	backend, err := NewXTestBackendWithConn(mc)
	if err != nil {
		b.Fatal(err)
	}
	backend.delay = 0
	if err := backend.TypeContext(context.Background(), "a"); err != nil {
		b.Fatalf("warmup TypeContext: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := backend.TypeContext(context.Background(), "a"); err != nil {
			b.Fatal(err)
		}
	}
}
