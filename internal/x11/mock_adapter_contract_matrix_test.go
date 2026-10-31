package x11

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
)

// The synthetic connection is the only mock shared by screen, window, output,
// and XTEST. Assert every reply and wire request rather than counting a mock
// invocation as successful production behavior.
func TestMockConnectionDefaultBehaviorAndCallbackOverrides(t *testing.T) {
	m := &MockConnection{}
	m.Close()
	m.Sync()
	if got := m.DefaultScreen(); got.Root != 1 || got.WidthInPixels != 800 || got.HeightInPixels != 600 {
		t.Fatalf("default screen %+v", got)
	}
	if got := m.Setup(); got.MinKeycode != 8 || got.MaxKeycode != 255 {
		t.Fatalf("default setup %+v", got)
	}
	if err := m.InitRandR(); err != nil {
		t.Fatal(err)
	}
	if got, err := m.GetScreenResourcesCurrent(42).Reply(); err != nil || got != nil {
		t.Fatalf("default resources %+v %v", got, err)
	}
	if got, err := m.GetOutputInfo(33, 44).Reply(); err != nil || got != nil {
		t.Fatalf("default output %+v %v", got, err)
	}
	if got, err := m.GetCrtcInfo(22, 44).Reply(); err != nil || got != nil {
		t.Fatalf("default CRTC %+v %v", got, err)
	}
	if got, err := m.GetOutputPrimary(42).Reply(); err != nil || got != nil {
		t.Fatalf("default primary %+v %v", got, err)
	}
	if got, err := m.InternAtom(false, 4, "TEST").Reply(); err != nil || got.Atom != 0 {
		t.Fatalf("default atom %+v %v", got, err)
	}
	if got, err := m.GetProperty(false, 42, 1, 2, 0, 8).Reply(); err != nil || got.Format != 32 || len(got.Value) != 0 {
		t.Fatalf("default property %+v %v", got, err)
	}
	if got, err := m.GetGeometry(42).Reply(); err != nil || got.Width != 0 {
		t.Fatalf("default geometry %+v %v", got, err)
	}
	if got, err := m.TranslateCoordinates(1, 2, 3, 4).Reply(); err != nil || got == nil {
		t.Fatalf("default translation %+v %v", got, err)
	}
	if got, err := m.QueryPointer(1).Reply(); err != nil || got == nil {
		t.Fatalf("default pointer %+v %v", got, err)
	}
	for _, check := range []struct {
		name   string
		cookie interface{ Check() error }
	}{
		{"send event", m.SendEventChecked(false, 2, 3, "event")},
		{"map window", m.MapWindowChecked(2)},
		{"configure window", m.ConfigureWindowChecked(2, 3, []uint32{4})},
		{"free pixmap", m.FreePixmap(3)},
		{"name pixmap", m.NameWindowPixmap(2, 3)},
	} {
		if err := check.cookie.Check(); err != nil {
			t.Fatalf("default checked %s: %v", check.name, err)
		}
	}
	if id, err := m.NewID(); err != nil || id != 42 {
		t.Fatalf("default generated X11 ID %d %v", id, err)
	}
	if err := m.InitComposite(); err != nil {
		t.Fatal(err)
	}
	if got, err := m.GetKeyboardMapping(8, 2).Reply(); err != nil || got.KeysymsPerKeycode != 1 {
		t.Fatalf("default keymap %+v %v", got, err)
	}
	if err := m.InitXTest(); err != nil {
		t.Fatal(err)
	}
	if err := m.FakeInputChecked(2, 38, 3, 4, 5, 6, 7).Check(); err != nil {
		t.Fatal(err)
	}
	if got := m.LastFakeInput; got.EventType != 2 || got.Detail != 38 || got.Time != 3 || got.Window != 4 || got.X != 5 || got.Y != 6 || got.Device != 7 {
		t.Fatalf("XTEST request lost parameters %+v", got)
	}
	// Default image cookie has no server; callers must see an error, not a
	// fabricated screen capture, when synthetic transport lacks a reply.
	if rep, err := m.GetImage(2, 3, 4, 5, 6, 7, ^uint32(0)).Reply(); err == nil && rep != nil && len(rep.Data) > 0 {
		t.Fatal("default image fabricated real pixel data")
	}
}
func TestMockConnectionOverridesPreserveAllProtocolArgumentsAndErrors(t *testing.T) {
	injected := errors.New("X11 synthetic remote transport failure")
	expectedScreen := &xproto.ScreenInfo{Root: 22, WidthInPixels: 1440, HeightInPixels: 900}
	expectedSetup := &xproto.SetupInfo{MinKeycode: 14, MaxKeycode: 67}
	m := &MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo { return expectedScreen },
		SetupFunc:         func() *xproto.SetupInfo { return expectedSetup },
		InitRandRFunc:     func() error { return injected },
		GetScreenResourcesCurrentFunc: func(w xproto.Window) RandRScreenResourcesCookie {
			if w != 22 {
				t.Fatalf("resources window %d", w)
			}
			return NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{ConfigTimestamp: 5}, nil)
		},
		GetOutputInfoFunc: func(o randr.Output, tm xproto.Timestamp) RandROutputInfoCookie {
			if o != 12 || tm != 5 {
				t.Fatalf("output args %d %d", o, tm)
			}
			return NewMockRandROutputInfoCookie(nil, injected)
		},
		GetCrtcInfoFunc: func(c randr.Crtc, tm xproto.Timestamp) RandRCrtcInfoCookie {
			if c != 11 || tm != 5 {
				t.Fatalf("crtc args %d %d", c, tm)
			}
			return NewMockRandRCrtcInfoCookie(nil, injected)
		},
		GetOutputPrimaryFunc: func(w xproto.Window) RandROutputPrimaryCookie {
			if w != 22 {
				t.Fatalf("primary arg %d", w)
			}
			return NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{Output: 12}, nil)
		},
		InternAtomFunc: func(only bool, n uint16, name string) InternAtomCookie {
			if !only || n != 4 || name != "TEST" {
				t.Fatalf("atom args %t %d %q", only, n, name)
			}
			return &MockInternAtomCookie{reply: &xproto.InternAtomReply{Atom: 19}}
		},
		GetPropertyFunc: func(del bool, w xproto.Window, prop, typ xproto.Atom, offset, length uint32) GetPropertyCookie {
			if !del || w != 22 || prop != 19 || typ != 4 || offset != 1 || length != 2 {
				t.Fatalf("property args %v %d %d %d %d %d", del, w, prop, typ, offset, length)
			}
			return NewMockGetPropertyCookieError(injected)
		},
		GetGeometryFunc: func(d xproto.Drawable) GetGeometryCookie {
			if d != 22 {
				t.Fatalf("geometry arg %d", d)
			}
			return NewMockGetGeometryCookie(&xproto.GetGeometryReply{Width: 640, Height: 480})
		},
		TranslateCoordinatesFunc: func(src, dst xproto.Window, x, y int16) TranslateCoordinatesCookie {
			if src != 1 || dst != 2 || x != -3 || y != 4 {
				t.Fatalf("translation args %d %d %d %d", src, dst, x, y)
			}
			return NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 9})
		},
		QueryPointerFunc: func(w xproto.Window) QueryPointerCookie {
			if w != 22 {
				t.Fatalf("pointer arg %d", w)
			}
			return &MockQueryPointerCookie{reply: &xproto.QueryPointerReply{RootX: 81, RootY: 42}}
		},
		SendEventCheckedFunc: func(p bool, w xproto.Window, mask uint32, event string) SendEventCookie {
			if !p || w != 22 || mask != 4 || event != "test-event" {
				t.Fatal("event request arguments lost")
			}
			return &MockXTestFakeInputCookie{err: injected}
		},
		MapWindowCheckedFunc: func(w xproto.Window) MapWindowCookie {
			if w != 22 {
				t.Fatal("map target lost")
			}
			return &MockXTestFakeInputCookie{err: injected}
		},
		ConfigureWindowCheckedFunc: func(w xproto.Window, mask uint16, values []uint32) ConfigureWindowCookie {
			if w != 22 || mask != 3 || !reflect.DeepEqual(values, []uint32{12, 15}) {
				t.Fatal("configure request args lost")
			}
			return &MockXTestFakeInputCookie{err: injected}
		},
		NewIDFunc: func() (uint32, error) { return 0, injected },
		GetImageFunc: func(format byte, d xproto.Drawable, x, y int16, w, h uint16, mask uint32) GetImageCookie {
			if format != 2 || d != 22 || x != -4 || y != 5 || w != 20 || h != 30 || mask != ^uint32(0) {
				t.Fatal("image request args lost")
			}
			return NewMockGetImageCookie(nil, injected)
		},
		FreePixmapFunc: func(p xproto.Pixmap) FreePixmapCookie {
			if p != 99 {
				t.Fatal("pixmap cleanup id lost")
			}
			return &MockXTestFakeInputCookie{err: injected}
		},
		InitCompositeFunc: func() error { return injected },
		NameWindowPixmapFunc: func(w xproto.Window, p xproto.Pixmap) NameWindowPixmapCookie {
			if w != 22 || p != 99 {
				t.Fatal("composite request args lost")
			}
			return &MockXTestFakeInputCookie{err: injected}
		},
		GetKeyboardMappingFunc: func(k xproto.Keycode, n byte) GetKeyboardMappingCookie {
			if k != 14 || n != 3 {
				t.Fatal("keyboard mapping args lost")
			}
			return NewMockGetKeyboardMappingErrorCookie(injected)
		},
		FakeInputCheckedFunc: func(event, detail byte, tm uint32, w xproto.Window, x, y int16, device byte) XTestFakeInputCookie {
			if event != 2 || detail != 38 || tm != 4 || w != 22 || x != 3 || y != -5 || device != 7 {
				t.Fatal("XTEST args lost")
			}
			return NewMockXTestFakeInputCookie(injected)
		},
	}
	if m.DefaultScreen() != expectedScreen || m.Setup() != expectedSetup {
		t.Fatal("custom X11 setup not forwarded")
	}
	if !errors.Is(m.InitRandR(), injected) {
		t.Fatal("RandR initialization error lost")
	}
	if rep, err := m.GetScreenResourcesCurrent(22).Reply(); err != nil || rep.ConfigTimestamp != 5 {
		t.Fatalf("RandR resources %v %v", rep, err)
	}
	if _, err := m.GetOutputInfo(12, 5).Reply(); !errors.Is(err, injected) {
		t.Fatal("RandR output error lost")
	}
	if _, err := m.GetCrtcInfo(11, 5).Reply(); !errors.Is(err, injected) {
		t.Fatal("RandR CRTC error lost")
	}
	if rep, err := m.GetOutputPrimary(22).Reply(); err != nil || rep.Output != 12 {
		t.Fatalf("RandR primary %+v %v", rep, err)
	}
	if rep, err := m.InternAtom(true, 4, "TEST").Reply(); err != nil || rep.Atom != 19 {
		t.Fatalf("atom %+v %v", rep, err)
	}
	if _, err := m.GetProperty(true, 22, 19, 4, 1, 2).Reply(); !errors.Is(err, injected) {
		t.Fatal("X11 property error lost")
	}
	if rep, err := m.GetGeometry(22).Reply(); err != nil || rep.Width != 640 {
		t.Fatalf("geometry %+v %v", rep, err)
	}
	if rep, err := m.TranslateCoordinates(1, 2, -3, 4).Reply(); err != nil || rep.DstX != 9 {
		t.Fatalf("translation %+v %v", rep, err)
	}
	if rep, err := m.QueryPointer(22).Reply(); err != nil || rep.RootX != 81 {
		t.Fatalf("pointer %+v %v", rep, err)
	}
	for _, ck := range []interface{ Check() error }{m.SendEventChecked(true, 22, 4, "test-event"), m.MapWindowChecked(22), m.ConfigureWindowChecked(22, 3, []uint32{12, 15}), m.FreePixmap(99), m.NameWindowPixmap(22, 99)} {
		if err := ck.Check(); !errors.Is(err, injected) {
			t.Fatalf("checked wire failure swallowed %v", err)
		}
	}
	if _, err := m.NewID(); !errors.Is(err, injected) {
		t.Fatal("X11 id allocation error lost")
	}
	if _, err := m.GetImage(2, 22, -4, 5, 20, 30, ^uint32(0)).Reply(); !errors.Is(err, injected) {
		t.Fatal("XGetImage failure lost")
	}
	if !errors.Is(m.InitComposite(), injected) {
		t.Fatal("Composite initialization error lost")
	}
	if _, err := m.GetKeyboardMapping(14, 3).Reply(); !errors.Is(err, injected) {
		t.Fatal("keyboard mapping error lost")
	}
	if err := m.FakeInputChecked(2, 38, 4, 22, 3, -5, 7).Check(); !errors.Is(err, injected) {
		t.Fatal("XTest failure lost")
	}
	if m.LastFakeInput.Window != 22 || m.LastFakeInput.X != 3 || m.LastFakeInput.Y != -5 {
		t.Fatalf("XTEST trace corrupted %+v", m.LastFakeInput)
	}
}
