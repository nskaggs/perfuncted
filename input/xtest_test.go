package input

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestKeyDownMapsToFakeInput(t *testing.T) {
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.SetupFunc = func() *xproto.SetupInfo { return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8} }
	mc.GetKeyboardMappingFunc = func(first xproto.Keycode, count byte) x11.GetKeyboardMappingCookie {
		// map the single keycode to keysym 'a' (0x61)
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0x61}})
	}
	mc.FakeInputCheckedFunc = func(eventType byte, detail byte, tm uint32, window xproto.Window, x, y int16, device byte) x11.XTestFakeInputCookie {
		return x11.NewMockXTestFakeInputCookie(nil)
	}

	b, err := NewXTestBackendWithConn(mc)
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("KeyDown failed: %v", err)
	}
	// ensure FakeInput was called and recorded
	if mc.LastFakeInput.EventType != xproto.KeyPress {
		t.Fatalf("expected KeyPress, got %d", mc.LastFakeInput.EventType)
	}
}
