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

// Mock connection for testing X11 backend.
type mockConnection struct {
	defaultScreenFn          func() *xproto.ScreenInfo
	internAtomFn             func(bool, uint16, string) x11.InternAtomCookie
	getPropertyFn            func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie
	getGeometryFn            func(xproto.Drawable) x11.GetGeometryCookie
	translateCoordinatesFn   func(xproto.Window, xproto.Window, int16, int16) x11.TranslateCoordinatesCookie
	sendEventCheckedFn       func(bool, xproto.Window, uint32, string) x11.SendEventCookie
	mapWindowCheckedFn       func(xproto.Window) x11.MapWindowCookie
	configureWindowCheckedFn func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie
	newIdFn                  func() (uint32, error)
}

func (m *mockConnection) Close() {}
func (m *mockConnection) DefaultScreen() *xproto.ScreenInfo {
	if m.defaultScreenFn != nil {
		return m.defaultScreenFn()
	}
	return &xproto.ScreenInfo{Root: 1, WidthInPixels: 1920, HeightInPixels: 1080}
}
func (m *mockConnection) InternAtom(o bool, l uint16, n string) x11.InternAtomCookie {
	if m.internAtomFn != nil {
		return m.internAtomFn(o, l, n)
	}
	return &mockInternAtomCookie{reply: &xproto.InternAtomReply{Atom: 1}}
}
func (m *mockConnection) GetProperty(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
	if m.getPropertyFn != nil {
		return m.getPropertyFn(d, w, p, t, lo, ll)
	}
	return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
}
func (m *mockConnection) GetGeometry(d xproto.Drawable) x11.GetGeometryCookie {
	if m.getGeometryFn != nil {
		return m.getGeometryFn(d)
	}
	return &mockGetGeometryCookie{reply: &xproto.GetGeometryReply{X: 0, Y: 0, Width: 100, Height: 100}}
}
func (m *mockConnection) TranslateCoordinates(s, d xproto.Window, x, y int16) x11.TranslateCoordinatesCookie {
	if m.translateCoordinatesFn != nil {
		return m.translateCoordinatesFn(s, d, x, y)
	}
	return &mockTranslateCoordinatesCookie{reply: &xproto.TranslateCoordinatesReply{DstX: 0, DstY: 0}}
}
func (m *mockConnection) SendEventChecked(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
	if m.sendEventCheckedFn != nil {
		return m.sendEventCheckedFn(p, dest, mask, ev)
	}
	return &mockSendEventCookie{}
}
func (m *mockConnection) MapWindowChecked(w xproto.Window) x11.MapWindowCookie {
	if m.mapWindowCheckedFn != nil {
		return m.mapWindowCheckedFn(w)
	}
	return &mockMapWindowCookie{}
}
func (m *mockConnection) ConfigureWindowChecked(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
	if m.configureWindowCheckedFn != nil {
		return m.configureWindowCheckedFn(w, mask, vals)
	}
	return &mockConfigureWindowCookie{}
}
func (m *mockConnection) NewId() (uint32, error) {
	if m.newIdFn != nil {
		return m.newIdFn()
	}
	return 999, nil
}
func (m *mockConnection) GetImage(b byte, d xproto.Drawable, x, y int16, w, h uint16, pm uint32) x11.GetImageCookie {
	return &mockGetImageCookie{}
}
func (m *mockConnection) FreePixmap(xproto.Pixmap) x11.FreePixmapCookie {
	return &mockFreePixmapCookie{}
}
func (m *mockConnection) InitComposite() error { return nil }
func (m *mockConnection) NameWindowPixmap(xproto.Window, xproto.Pixmap) x11.NameWindowPixmapCookie {
	return &mockNameWindowPixmapCookie{}
}

// Mock cookies.
type mockInternAtomCookie struct {
	reply *xproto.InternAtomReply
	err   error
}

func (m *mockInternAtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return m.reply, m.err
}

type mockGetPropertyCookie struct {
	reply *xproto.GetPropertyReply
	err   error
}

func (m *mockGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) {
	return m.reply, m.err
}

type mockGetGeometryCookie struct {
	reply *xproto.GetGeometryReply
	err   error
}

func (m *mockGetGeometryCookie) Reply() (*xproto.GetGeometryReply, error) {
	return m.reply, m.err
}

type mockTranslateCoordinatesCookie struct {
	reply *xproto.TranslateCoordinatesReply
	err   error
}

func (m *mockTranslateCoordinatesCookie) Reply() (*xproto.TranslateCoordinatesReply, error) {
	return m.reply, m.err
}

type mockSendEventCookie struct{ err error }

func (m *mockSendEventCookie) Check() error { return m.err }

type mockMapWindowCookie struct{ err error }

func (m *mockMapWindowCookie) Check() error { return m.err }

type mockConfigureWindowCookie struct{ err error }

func (m *mockConfigureWindowCookie) Check() error { return m.err }

type mockGetImageCookie struct{}

func (m *mockGetImageCookie) Reply() (*xproto.GetImageReply, error) {
	return &xproto.GetImageReply{}, nil
}

type mockFreePixmapCookie struct{ err error }

func (m *mockFreePixmapCookie) Check() error { return m.err }

type mockNameWindowPixmapCookie struct{ err error }

func (m *mockNameWindowPixmapCookie) Check() error { return m.err }

// newStubX11Backend creates a test X11Backend with mock connection and atoms.
func newStubX11Backend(t *testing.T, withWindow bool, title string) *X11Backend {
	t.Helper()
	conn := &mockConnection{}
	b := &X11Backend{
		conn:                conn,
		root:                1,
		atomNetClientList:   10,
		atomNetActiveWindow: 11,
		atomNetFrameExtent:  12,
		atomNetWMName:       13,
		atomNetWMPID:        14,
		atomMotifWMHints:    15,
		atomUTF8String:      16,
	}
	conn.defaultScreenFn = func() *xproto.ScreenInfo {
		return &xproto.ScreenInfo{Root: b.root, WidthInPixels: 1920, HeightInPixels: 1080}
	}
	if withWindow {
		wid := xproto.Window(50)
		titleBytes := []byte(title)
		conn.getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
			switch p {
			case b.atomNetClientList:
				return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}}}
			case b.atomNetWMName:
				return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 8, Value: titleBytes}}
			case b.atomNetWMPID:
				return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{1, 0, 0, 0}}}
			case b.atomNetActiveWindow:
				return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
			case b.atomNetFrameExtent, b.atomMotifWMHints:
				return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
			}
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
		}
		conn.getGeometryFn = func(d xproto.Drawable) x11.GetGeometryCookie {
			return &mockGetGeometryCookie{reply: &xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600}}
		}
		conn.translateCoordinatesFn = func(s, d xproto.Window, x, y int16) x11.TranslateCoordinatesCookie {
			return &mockTranslateCoordinatesCookie{reply: &xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20}}
		}
	}
	return b
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
	b := newStubX11Backend(t, true, "Test Window")
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 1 || wins[0].Title != "Test Window" {
		t.Errorf("List() = %+v, want 1 window titled %q", wins, "Test Window")
	}
}

func TestX11Backend_List_Empty(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
	}
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 0 {
		t.Errorf("List() expected 0 windows, got %d", len(wins))
	}
}

func TestX11Backend_List_GetPropertyError(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return &mockGetPropertyCookie{err: fmt.Errorf("simulated error")}
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get _NET_CLIENT_LIST") {
		t.Errorf("List() error = %v, want error containing %q", err, "get _NET_CLIENT_LIST")
	}
}

func TestX11Backend_List_UnexpectedFormat(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 8, Value: []byte{1, 2, 3}}}
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected _NET_CLIENT_LIST format") {
		t.Errorf("List() error = %v, want error containing format", err)
	}
}

func runX11ActionTest(t *testing.T, name, title string, windowExists bool, action func(*X11Backend, context.Context, string) error) {
	t.Helper()
	t.Run("Window exists", func(t *testing.T) {
		var events []string
		b := newStubX11Backend(t, true, title)
		b.conn.(*mockConnection).sendEventCheckedFn = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &mockSendEventCookie{}
		}
		if err := action(b, context.Background(), title); err != nil {
			t.Errorf("%s() unexpected error: %v", name, err)
		}
		if len(events) == 0 {
			t.Errorf("%s() did not send event", name)
		}
	})
	t.Run("Window does not exist", func(t *testing.T) {
		b := newStubX11Backend(t, false, "")
		err := action(b, context.Background(), "Nonexistent")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s() error = %v, want error containing %q", name, err, "not found")
		}
	})
}

func TestX11Backend_Activate(t *testing.T) {
	runX11ActionTest(t, "Activate", "ActivateMe", true, func(b *X11Backend, ctx context.Context, title string) error {
		return b.Activate(ctx, title)
	})
}

func TestX11Backend_CloseWindow(t *testing.T) {
	runX11ActionTest(t, "CloseWindow", "CloseMe", true, func(b *X11Backend, ctx context.Context, title string) error {
		return b.CloseWindow(ctx, title)
	})
}

func TestX11Backend_Minimize(t *testing.T) {
	runX11ActionTest(t, "Minimize", "MinimizeMe", true, func(b *X11Backend, ctx context.Context, title string) error {
		return b.Minimize(ctx, title)
	})
}

func TestX11Backend_Maximize(t *testing.T) {
	runX11ActionTest(t, "Maximize", "MaximizeMe", true, func(b *X11Backend, ctx context.Context, title string) error {
		return b.Maximize(ctx, title)
	})
}

func TestX11Backend_Restore(t *testing.T) {
	t.Run("Window exists", func(t *testing.T) {
		var events []string
		var mapped []xproto.Window
		b := newStubX11Backend(t, true, "RestoreMe")
		b.conn.(*mockConnection).sendEventCheckedFn = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &mockSendEventCookie{}
		}
		b.conn.(*mockConnection).mapWindowCheckedFn = func(w xproto.Window) x11.MapWindowCookie {
			mapped = append(mapped, w)
			return &mockMapWindowCookie{}
		}
		if err := b.Restore(context.Background(), "RestoreMe"); err != nil {
			t.Errorf("Restore() unexpected error: %v", err)
		}
		if len(events) == 0 {
			t.Errorf("Restore() did not send event")
		}
		if len(mapped) == 0 {
			t.Errorf("Restore() did not map window")
		}
	})
	t.Run("Window does not exist", func(t *testing.T) {
		b := newStubX11Backend(t, false, "")
		err := b.Restore(context.Background(), "Nonexistent")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("Restore() error = %v, want error containing %q", err, "not found")
		}
	})
}

func TestX11Backend_Move(t *testing.T) {
	t.Run("Window exists", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b := newStubX11Backend(t, true, "MoveMe")
		b.conn.(*mockConnection).configureWindowCheckedFn = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &mockConfigureWindowCookie{}
		}
		if err := b.Move(context.Background(), "MoveMe", 100, 200); err != nil {
			t.Errorf("Move() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("Move() did not configure window")
		}
	})
	t.Run("Window does not exist", func(t *testing.T) {
		b := newStubX11Backend(t, false, "")
		err := b.Move(context.Background(), "Nonexistent", 100, 200)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("Move() error = %v, want error containing %q", err, "not found")
		}
	})
}

func TestX11Backend_Resize(t *testing.T) {
	t.Run("Window exists", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b := newStubX11Backend(t, true, "ResizeMe")
		b.conn.(*mockConnection).configureWindowCheckedFn = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &mockConfigureWindowCookie{}
		}
		if err := b.Resize(context.Background(), "ResizeMe", 1024, 768); err != nil {
			t.Errorf("Resize() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("Resize() did not configure window")
		}
	})
	t.Run("Window does not exist", func(t *testing.T) {
		b := newStubX11Backend(t, false, "")
		err := b.Resize(context.Background(), "Nonexistent", 1024, 768)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("Resize() error = %v, want error containing %q", err, "not found")
		}
	})
}

func TestX11Backend_ActiveTitle(t *testing.T) {
	b := newStubX11Backend(t, true, "Active Window")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			wid := xproto.Window(50)
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}}}
		}
		if p == b.atomNetWMName {
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 8, Value: []byte("Active Window")}}
		}
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
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
	b := newStubX11Backend(t, false, "")
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("ActiveTitle() unexpected error: %v", err)
	}
	if title != "" {
		t.Errorf("ActiveTitle() = %q, want empty", title)
	}
}

func TestX11Backend_findByTitle(t *testing.T) {
	b := newStubX11Backend(t, true, "My Test Window")
	win, err := b.findByTitle(context.Background(), "My Test Window")
	if err != nil {
		t.Errorf("findByTitle(exact) unexpected error: %v", err)
	}
	if win != xproto.Window(50) {
		t.Errorf("findByTitle(exact) = %d, want 50", win)
	}
	_, err = b.findByTitle(context.Background(), "Nonexistent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("findByTitle(no match) error = %v, want error containing %q", err, "not found")
	}
}

func TestX11Backend_windowTitle(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMName {
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 8, Value: []byte("UTF8 Title")}}
		}
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
	}
	if title := b.windowTitle(50); title != "UTF8 Title" {
		t.Errorf("windowTitle() = %q, want %q", title, "UTF8 Title")
	}
}

func TestX11Backend_windowPID(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMPID {
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{42, 0, 0, 0}}}
		}
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
	}
	if pid := b.windowPID(50); pid != 42 {
		t.Errorf("windowPID() = %d, want 42", pid)
	}
}

func TestX11Backend_windowPID_NoPID(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	if pid := b.windowPID(50); pid != 0 {
		t.Errorf("windowPID() = %d, want 0", pid)
	}
}

func TestX11Backend_windowHasDecoration(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 1 // decorations = 1
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: data}}
		}
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
	}
	if !b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = false, want true")
	}
}

func TestX11Backend_windowHasDecoration_NoDecoration(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getPropertyFn = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 0 // decorations = 0
			return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: data}}
		}
		return &mockGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
	}
	if b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = true, want false")
	}
}

func TestX11Backend_windowGeometry(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	b.conn.(*mockConnection).getGeometryFn = func(d xproto.Drawable) x11.GetGeometryCookie {
		return &mockGetGeometryCookie{reply: &xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600}}
	}
	b.conn.(*mockConnection).translateCoordinatesFn = func(s, d xproto.Window, x, y int16) x11.TranslateCoordinatesCookie {
		return &mockTranslateCoordinatesCookie{reply: &xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20}}
	}
	x, y, w, h := b.windowGeometry(50)
	if x != 10 || y != 20 || w != 800 || h != 600 {
		t.Errorf("windowGeometry() = (%d, %d, %d, %d), want (10, 20, 800, 600)", x, y, w, h)
	}
}

func TestX11Backend_Close(t *testing.T) {
	b := newStubX11Backend(t, false, "")
	if err := b.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}
