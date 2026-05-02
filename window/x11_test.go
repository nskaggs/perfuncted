package window

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestActiveTitle(t *testing.T) {
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.GetPropertyFunc = func(Delete bool, Window xproto.Window, Property, Type xproto.Atom, LongOffset, LongLength uint32) x11.GetPropertyCookie {
		// _NET_ACTIVE_WINDOW
		if Property == 1000 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte{2, 0, 0, 0}, Format: 32})
		}
		// _NET_WM_NAME
		if Property == 1001 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("My Window")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte{}})
	}

	b := NewX11BackendWithConn(mc)
	b.atomNetActiveWindow = xproto.Atom(1000)
	b.atomNetWMName = xproto.Atom(1001)
	b.atomUTF8String = xproto.Atom(0)

	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "My Window" {
		t.Fatalf("expected 'My Window', got %q", title)
	}
}

func TestActiveTitle_NoActiveWindow(t *testing.T) {
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.GetPropertyFunc = func(_ bool, _ xproto.Window, _ xproto.Atom, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte{}})
	}
	b := NewX11BackendWithConn(mc)
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "" {
		t.Fatalf("expected empty title, got %q", title)
	}
}

func TestIterateWindows(t *testing.T) {
	// Encode two window IDs in the _NET_CLIENT_LIST reply value.
	winIDs := []uint32{10, 20}
	val := make([]byte, 8)
	binary.LittleEndian.PutUint32(val[0:], winIDs[0])
	binary.LittleEndian.PutUint32(val[4:], winIDs[1])

	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.GetPropertyFunc = func(_ bool, win xproto.Window, prop xproto.Atom, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		// _NET_CLIENT_LIST on root
		if win == 1 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: val})
		}
		// _NET_WM_NAME on each window
		if win == 10 {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("win10")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("win20")})
	}
	mc.GetGeometryFunc = func(_ xproto.Drawable) x11.GetGeometryCookie {
		return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{Width: 100, Height: 200})
	}
	mc.TranslateCoordinatesFunc = func(_, _ xproto.Window, _, _ int16) x11.TranslateCoordinatesCookie {
		return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 5, DstY: 10})
	}

	b := NewX11BackendWithConn(mc)
	b.atomNetClientList = xproto.Atom(100)

	var titles []string
	for info, err := range b.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		titles = append(titles, info.Title)
	}
	if len(titles) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(titles))
	}
}

func TestMove(t *testing.T) {
	var gotMask uint16
	var gotValues []uint32

	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.ConfigureWindowCheckedFunc = func(_ xproto.Window, mask uint16, values []uint32) x11.ConfigureWindowCookie {
		gotMask = mask
		gotValues = values
		return &x11.MockCheckCookie{}
	}
	// FindByTitle needs IterateWindows → _NET_CLIENT_LIST
	mc.GetPropertyFunc = func(_ bool, win xproto.Window, prop xproto.Atom, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if win == 1 { // root: return one window ID=42
			v := make([]byte, 4)
			binary.LittleEndian.PutUint32(v, 42)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v})
		}
		// _NET_WM_NAME for window 42
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("target")})
	}
	mc.GetGeometryFunc = func(_ xproto.Drawable) x11.GetGeometryCookie {
		return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{Width: 100, Height: 100})
	}
	mc.TranslateCoordinatesFunc = func(_, _ xproto.Window, _, _ int16) x11.TranslateCoordinatesCookie {
		return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{})
	}

	b := NewX11BackendWithConn(mc)
	if err := b.Move(context.Background(), "target", 50, 80); err != nil {
		t.Fatalf("Move() error: %v", err)
	}
	wantMask := uint16(xproto.ConfigWindowX | xproto.ConfigWindowY)
	if gotMask != wantMask {
		t.Errorf("ConfigureWindowChecked mask = 0x%x, want 0x%x", gotMask, wantMask)
	}
	if len(gotValues) != 2 || gotValues[0] != 50 || gotValues[1] != 80 {
		t.Errorf("ConfigureWindowChecked values = %v, want [50 80]", gotValues)
	}
}

func TestResize(t *testing.T) {
	var gotValues []uint32

	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return &xproto.ScreenInfo{Root: xproto.Window(1)} }
	mc.ConfigureWindowCheckedFunc = func(_ xproto.Window, _ uint16, values []uint32) x11.ConfigureWindowCookie {
		gotValues = values
		return &x11.MockCheckCookie{}
	}
	mc.GetPropertyFunc = func(_ bool, win xproto.Window, _ xproto.Atom, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if win == 1 {
			v := make([]byte, 4)
			binary.LittleEndian.PutUint32(v, 42)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Value: []byte("resize-me")})
	}
	mc.GetGeometryFunc = func(_ xproto.Drawable) x11.GetGeometryCookie {
		return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{})
	}
	mc.TranslateCoordinatesFunc = func(_, _ xproto.Window, _, _ int16) x11.TranslateCoordinatesCookie {
		return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{})
	}

	b := NewX11BackendWithConn(mc)
	if err := b.Resize(context.Background(), "resize-me", 640, 480); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	if len(gotValues) != 2 || gotValues[0] != 640 || gotValues[1] != 480 {
		t.Errorf("ConfigureWindowChecked values = %v, want [640 480]", gotValues)
	}
}

