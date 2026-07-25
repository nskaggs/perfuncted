//go:build linux
// +build linux

package window

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// newStubX11Backend creates an X11Backend backed by a shared MockConnection with
// pre-set atom values.  Tests that need to inspect or override specific methods
// receive the *x11.MockConnection directly so they can set Func hooks without
// any type-assertion.
func newStubX11Backend(t *testing.T, withWindow bool, title string) (*X11Backend, *x11.MockConnection) {
	t.Helper()
	conn := &x11.MockConnection{}
	b := &X11Backend{
		conn:                        conn,
		root:                        1,
		atomNetClientList:           10,
		atomNetActiveWindow:         11,
		atomNetFrameExtent:          12,
		atomNetWMName:               13,
		atomNetWMState:              20,
		atomNetWMStateHidden:        21,
		atomNetWMStateMaximizedVert: 22,
		atomNetWMStateMaximizedHorz: 23,
		atomNetWMPID:                14,
		atomMotifWMHints:            15,
		atomUTF8String:              16,
	}
	conn.DefaultScreenFunc = func() *xproto.ScreenInfo {
		return &xproto.ScreenInfo{Root: b.root, WidthInPixels: 1920, HeightInPixels: 1080}
	}
	// Default: return an empty client list (Format 32, zero bytes) so that
	// IterateWindows/findByTitle return "not found" rather than a format error.
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if withWindow {
		wid := xproto.Window(50)
		titleBytes := []byte(title)
		conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
			switch p {
			case b.atomNetClientList:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
			case b.atomNetWMName:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: titleBytes})
			case b.atomNetWMPID:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{1, 0, 0, 0}})
			case b.atomNetActiveWindow:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
			case b.atomNetFrameExtent, b.atomMotifWMHints:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
		}
		conn.GetGeometryFunc = func(d xproto.Drawable) x11.GetGeometryCookie {
			return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
		}
		conn.TranslateCoordinatesFunc = func(s, d xproto.Window, sx, sy int16) x11.TranslateCoordinatesCookie {
			return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20})
		}
	}
	return b, conn
}

func TestX11Backend_New_NoDisplay(t *testing.T) {
	orig := os.Getenv("DISPLAY")
	os.Unsetenv("DISPLAY")
	defer os.Setenv("DISPLAY", orig)
	_, err := NewX11Backend("")
	if err == nil || !strings.Contains(err.Error(), "window/x11: connect to display") {
		t.Fatalf("NewX11Backend() error = %v, want connection error", err)
	}
}

func TestX11Backend_List(t *testing.T) {
	b, _ := newStubX11Backend(t, true, "Test Window")
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 1 || wins[0].Title != "Test Window" {
		t.Errorf("List() = %+v, want 1 window titled %q", wins, "Test Window")
	}
}

func TestX11Backend_List_Empty(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 0 {
		t.Errorf("List() expected 0 windows, got %d", len(wins))
	}
}

func TestX11Backend_List_GetPropertyError(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookieError(fmt.Errorf("simulated error"))
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get _NET_CLIENT_LIST") {
		t.Errorf("List() error = %v, want error containing %q", err, "get _NET_CLIENT_LIST")
	}
}

func TestX11Backend_List_UnexpectedFormat(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte{1, 2, 3}})
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected _NET_CLIENT_LIST format") {
		t.Errorf("List() error = %v, want error containing format", err)
	}
}

// runX11ActionTest is a table-helper for ID-based actions that send an event.
func runX11ActionTest(t *testing.T, name string, action func(*X11Backend, context.Context, string) error) {
	t.Helper()
	t.Run("valid ID", func(t *testing.T) {
		var events []string
		b, conn := newStubX11Backend(t, true, name)
		conn.SendEventCheckedFunc = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &x11.MockCheckCookie{}
		}
		if err := action(b, context.Background(), "50"); err != nil {
			t.Errorf("%s() unexpected error: %v", name, err)
		}
		if len(events) == 0 {
			t.Errorf("%s() did not send event", name)
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		err := action(b, context.Background(), "not-a-number")
		if err == nil {
			t.Errorf("%s() error = nil, want invalid ID error", name)
		}
	})
}

func TestX11Backend_ActivateByID(t *testing.T) {
	runX11ActionTest(t, "ActivateByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.ActivateByID(ctx, id)
	})
}

func TestX11Backend_CloseWindowByID(t *testing.T) {
	runX11ActionTest(t, "CloseWindowByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.CloseWindowByID(ctx, id)
	})
}

func TestX11Backend_MinimizeByID(t *testing.T) {
	runX11ActionTest(t, "MinimizeByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.MinimizeByID(ctx, id)
	})
}

func TestX11Backend_MaximizeByID(t *testing.T) {
	runX11ActionTest(t, "MaximizeByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.MaximizeByID(ctx, id)
	})
}

func TestX11Backend_RestoreByID(t *testing.T) {
	t.Run("valid ID", func(t *testing.T) {
		var events []string
		var mapped []xproto.Window
		b, conn := newStubX11Backend(t, true, "RestoreByID")
		conn.SendEventCheckedFunc = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &x11.MockCheckCookie{}
		}
		conn.MapWindowCheckedFunc = func(w xproto.Window) x11.MapWindowCookie {
			mapped = append(mapped, w)
			return &x11.MockCheckCookie{}
		}
		if err := b.RestoreByID(context.Background(), "50"); err != nil {
			t.Errorf("RestoreByID() unexpected error: %v", err)
		}
		if len(events) == 0 {
			t.Errorf("RestoreByID() did not send event")
		}
		if len(mapped) == 0 {
			t.Errorf("RestoreByID() did not map window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.RestoreByID(context.Background(), "not-a-number"); err == nil {
			t.Error("RestoreByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_MoveByID(t *testing.T) { //nolint:dupl
	t.Run("valid ID", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b, conn := newStubX11Backend(t, true, "MoveMe")
		conn.ConfigureWindowCheckedFunc = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &x11.MockCheckCookie{}
		}
		if err := b.MoveByID(context.Background(), "50", 100, 200); err != nil {
			t.Errorf("MoveByID() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("MoveByID() did not configure window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.MoveByID(context.Background(), "not-a-number", 100, 200); err == nil {
			t.Error("MoveByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_ResizeByID(t *testing.T) { //nolint:dupl
	t.Run("valid ID", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b, conn := newStubX11Backend(t, true, "ResizeMe")
		conn.ConfigureWindowCheckedFunc = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &x11.MockCheckCookie{}
		}
		if err := b.ResizeByID(context.Background(), "50", 1024, 768); err != nil {
			t.Errorf("ResizeByID() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("ResizeByID() did not configure window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.ResizeByID(context.Background(), "not-a-number", 1024, 768); err == nil {
			t.Error("ResizeByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_ActiveTitle(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Active Window")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			wid := xproto.Window(50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
		}
		if p == b.atomNetWMName {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Active Window")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("ActiveTitle() unexpected error: %v", err)
	}
	if title != "Active Window" {
		t.Errorf("ActiveTitle() = %q, want %q", title, "Active Window")
	}
}

func TestX11Backend_ActiveTitle_NoActive(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("ActiveTitle() unexpected error: %v", err)
	}
	if title != "" {
		t.Errorf("ActiveTitle() = %q, want empty", title)
	}
}

func TestX11Backend_windowTitle(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMName {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("UTF8 Title")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if title := b.windowTitle(50); title != "UTF8 Title" {
		t.Errorf("windowTitle() = %q, want %q", title, "UTF8 Title")
	}
}

func TestX11Backend_windowPID(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMPID {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{42, 0, 0, 0}})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if pid := b.windowPID(50); pid != 42 {
		t.Errorf("windowPID() = %d, want 42", pid)
	}
}

func TestX11Backend_windowPID_NoPID(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	if pid := b.windowPID(50); pid != 0 {
		t.Errorf("windowPID() = %d, want 0", pid)
	}
}

func TestX11Backend_windowClass(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == xproto.AtomWmClass {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("org.example\x00Example\x00")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	appID, class := b.windowClass(50)
	if appID != "org.example" || class != "Example" {
		t.Fatalf("windowClass() = (%q, %q), want (%q, %q)", appID, class, "org.example", "Example")
	}
}

func TestX11Backend_windowHasDecoration(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 1 // decorations = 1
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if !b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = false, want true")
	}
}

func TestX11Backend_windowHasDecoration_NoDecoration(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 0 // decorations = 0
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = true, want false")
	}
}

func TestX11Backend_windowGeometry(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetGeometryFunc = func(d xproto.Drawable) x11.GetGeometryCookie {
		return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
	}
	conn.TranslateCoordinatesFunc = func(s, d xproto.Window, x, y int16) x11.TranslateCoordinatesCookie {
		return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20})
	}
	x, y, w, h := b.windowGeometry(50)
	if x != 10 || y != 20 || w != 800 || h != 600 {
		t.Errorf("windowGeometry() = (%d, %d, %d, %d), want (10, 20, 800, 600)", x, y, w, h)
	}
}

func TestX11Backend_Close(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	if err := b.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

func TestX11Backend_activeWindow(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	// Test with active window
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			wid := xproto.Window(50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	win, err := b.activeWindow()
	if err != nil {
		t.Fatalf("activeWindow() unexpected error: %v", err)
	}
	if win != xproto.Window(50) {
		t.Errorf("activeWindow() = %d, want 50", win)
	}
}

func TestX11Backend_activeWindow_NoActive(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	win, err := b.activeWindow()
	if err != nil {
		t.Fatalf("activeWindow() unexpected error: %v", err)
	}
	if win != 0 {
		t.Errorf("activeWindow() = %d, want 0", win)
	}
}

func TestX11Backend_activeWindow_Error(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookieError(fmt.Errorf("connection error"))
	}
	_, err := b.activeWindow()
	if err == nil {
		t.Fatal("activeWindow() expected error, got nil")
	}
}

func TestX11Backend_windowState_Minimized(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			// Return _NET_WM_STATE_HIDDEN in the state
			atomBytes := []byte{
				byte(b.atomNetWMStateHidden), byte(b.atomNetWMStateHidden >> 8),
				byte(b.atomNetWMStateHidden >> 16), byte(b.atomNetWMStateHidden >> 24),
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized := b.windowState(50)
	if !minimized {
		t.Errorf("windowState() minimized = false, want true")
	}
	if maximized {
		t.Errorf("windowState() maximized = true, want false")
	}
}

func TestX11Backend_windowState_Maximized(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			// Return both maximized vert and horz (8 bytes = 2 atoms * 4 bytes each)
			atomBytes := make([]byte, 8)
			// _NET_WM_STATE_MAXIMIZED_VERT
			atomBytes[0] = byte(b.atomNetWMStateMaximizedVert)
			atomBytes[1] = byte(b.atomNetWMStateMaximizedVert >> 8)
			atomBytes[2] = byte(b.atomNetWMStateMaximizedVert >> 16)
			atomBytes[3] = byte(b.atomNetWMStateMaximizedVert >> 24)
			// _NET_WM_STATE_MAXIMIZED_HORZ
			atomBytes[4] = byte(b.atomNetWMStateMaximizedHorz)
			atomBytes[5] = byte(b.atomNetWMStateMaximizedHorz >> 8)
			atomBytes[6] = byte(b.atomNetWMStateMaximizedHorz >> 16)
			atomBytes[7] = byte(b.atomNetWMStateMaximizedHorz >> 24)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized := b.windowState(50)
	if minimized {
		t.Errorf("windowState() minimized = true, want false")
	}
	if !maximized {
		t.Errorf("windowState() maximized = false, want true")
	}
}

// encodeWMStateAtoms encodes three atom uint32 values into a 12-byte little-endian slice
// suitable for a _NET_WM_STATE property reply.
func encodeWMStateAtoms(a, b, c xproto.Atom) []byte {
	atomBytes := make([]byte, 12)
	atomBytes[0] = byte(a)
	atomBytes[1] = byte(a >> 8)
	atomBytes[2] = byte(a >> 16)
	atomBytes[3] = byte(a >> 24)
	atomBytes[4] = byte(b)
	atomBytes[5] = byte(b >> 8)
	atomBytes[6] = byte(b >> 16)
	atomBytes[7] = byte(b >> 24)
	atomBytes[8] = byte(c)
	atomBytes[9] = byte(c >> 8)
	atomBytes[10] = byte(c >> 16)
	atomBytes[11] = byte(c >> 24)
	return atomBytes
}

func TestX11Backend_windowState_Both(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := encodeWMStateAtoms(b.atomNetWMStateHidden, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized := b.windowState(50)
	if !minimized {
		t.Errorf("windowState() minimized = false, want true")
	}
	if !maximized {
		t.Errorf("windowState() maximized = false, want true")
	}
}

func TestX11Backend_windowState_Error(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			return x11.NewMockGetPropertyCookieError(fmt.Errorf("connection error"))
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized := b.windowState(50)
	if minimized || maximized {
		t.Errorf("windowState() with error should return false, false, got %v, %v", minimized, maximized)
	}
}

func TestX11Backend_IterateWindows_MinimizedMaximized(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Test Window")
	// Override getPropertyFn to also handle atomNetWMState
	origFn := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := encodeWMStateAtoms(b.atomNetWMStateHidden, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		// Call original function for other properties
		return origFn(d, w, p, t, lo, ll)
	}
	ctx := context.Background()
	var found Info
	for info, err := range b.IterateWindows(ctx) {
		if err != nil {
			t.Fatalf("IterateWindows() error: %v", err)
		}
		found = info
	}
	if !found.Minimized {
		t.Errorf("IterateWindows() Minimized = false, want true")
	}
	if !found.Maximized {
		t.Errorf("IterateWindows() Maximized = false, want true")
	}
}
