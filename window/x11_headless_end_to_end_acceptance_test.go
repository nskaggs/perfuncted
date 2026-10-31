//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestX11ManagerHeadlessEWMHDiscoveryGeometryAndActions(t *testing.T) {
	privateAuth := filepath.Join(t.TempDir(), "isolated-auth")
	if err := os.WriteFile(privateAuth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", privateAuth)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("disposable Xvfb display unavailable: %v", err)
	}
	t.Cleanup(stop)
	raw, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	screen := xproto.Setup(raw).DefaultScreen(raw)
	if screen == nil {
		t.Fatal("Xvfb has no root display")
	}
	winID, err := raw.NewId()
	if err != nil {
		t.Fatal(err)
	}
	win := xproto.Window(winID)
	if lintShadow45 := xproto.CreateWindowChecked(raw, screen.RootDepth, win, screen.Root, 11, 17, 130, 90, 0, xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); lintShadow45 != nil {
		t.Fatal(lintShadow45)
	}
	defer xproto.DestroyWindow(raw, win)
	if lintShadow49 := xproto.MapWindowChecked(raw, win).Check(); lintShadow49 != nil {
		t.Fatal(lintShadow49)
	}
	backend, err := NewX11Backend(display)
	if err != nil {
		t.Fatalf("open X11 manager on synthetic desktop: %v", err)
	}
	defer backend.Close()
	setProperty := func(w xproto.Window, p, k xproto.Atom, format byte, items uint32, data []byte) {
		t.Helper()
		if lintShadow59 := xproto.ChangePropertyChecked(raw, xproto.PropModeReplace, w, p, k, format, items, data).Check(); lintShadow59 != nil {
			t.Fatalf("set X11 root/window property %d: %v", p, lintShadow59)
		}
	}
	atomBytes := func(ids ...uint32) []byte {
		buf := make([]byte, 4*len(ids))
		for i, id := range ids {
			binary.LittleEndian.PutUint32(buf[i*4:], id)
		}
		return buf
	}
	setProperty(screen.Root, backend.atomNetClientList, xproto.AtomWindow, 32, 1, atomBytes(winID))
	setProperty(screen.Root, backend.atomNetActiveWindow, xproto.AtomWindow, 32, 1, atomBytes(winID))
	setProperty(win, backend.atomNetWMName, backend.atomUTF8String, 8, 11, []byte("Test Editor"))
	setProperty(win, xproto.AtomWmClass, xproto.AtomString, 8, uint32(len("pf-test\x00PFTest\x00")), []byte("pf-test\x00PFTest\x00"))
	setProperty(win, backend.atomNetWMPID, backend.atomNetWMPID, 32, 1, atomBytes(1234))
	setProperty(win, backend.atomNetWMState, xproto.AtomAtom, 32, 3, atomBytes(uint32(backend.atomNetWMStateMaximizedVert), uint32(backend.atomNetWMStateMaximizedHorz), uint32(backend.atomNetWMStateFullscreen)))
	ctx := context.Background()
	windows, err := backend.List(ctx)
	if err != nil || len(windows) != 1 {
		t.Fatalf("real X11 root enumeration: %+v %v", windows, err)
	}
	info := windows[0]
	if info.ID != uint64(winID) || info.Title != "Test Editor" || info.AppID != "pf-test" || info.Class != "PFTest" || info.PID != 1234 || !info.Active || !info.Maximized || !info.Fullscreen || info.Minimized {
		t.Fatalf("EWMH window metadata was lost: %+v", info)
	}
	title, err := backend.ActiveTitle(ctx)
	if err != nil || title != "Test Editor" {
		t.Fatalf("root active window: %q %v", title, err)
	}
	key := strconv.FormatUint(uint64(winID), 10)
	fresh, err := backend.InfoByID(ctx, key)
	if err != nil || fresh.ID != uint64(winID) {
		t.Fatalf("InfoByID %s: %+v %v", key, fresh, err)
	}
	if _, lintShadow94 := backend.InfoByID(ctx, "999999"); !errors.Is(lintShadow94, ErrWindowNotFound) {
		t.Fatalf("unknown window accepted: %v", lintShadow94)
	}
	if lintShadow97 := backend.MoveByID(ctx, key, 71, 53); lintShadow97 != nil {
		t.Fatalf("real X11 move failed: %v", lintShadow97)
	}
	if lintShadow100 := backend.ResizeByID(ctx, key, 215, 145); lintShadow100 != nil {
		t.Fatalf("real X11 resize failed: %v", lintShadow100)
	}
	geom, err := xproto.GetGeometry(raw, xproto.Drawable(win)).Reply()
	if err != nil || geom.X != 71 || geom.Y != 53 || geom.Width != 215 || geom.Height != 145 {
		t.Fatalf("server did not apply requested geometry: %+v %v", geom, err)
	}
	for _, action := range []struct {
		name string
		call func(context.Context, string) error
	}{
		{"activate", backend.ActivateByID}, {"minimize", backend.MinimizeByID}, {"maximize", backend.MaximizeByID},
		{"fullscreen", backend.FullscreenByID}, {"unfullscreen", backend.UnfullscreenByID}, {"restore", backend.RestoreByID}, {"close", backend.CloseWindowByID},
	} {
		t.Run(action.name, func(t *testing.T) {
			if err := action.call(ctx, key); err != nil {
				t.Fatalf("EWMH request could not be serialized: %v", err)
			}
		})
	}
	if err := backend.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if ops := backend.SupportedOperations(); len(ops) < 10 {
		t.Fatalf("missing X11 control capabilities: %v", ops)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.MoveByID(canceled, key, 1, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled movement: %v", err)
	}
	if err := backend.ResizeByID(ctx, key, 0, 10); err == nil {
		t.Fatal("invalid X11 resize was sent")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.List(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed X11 manager accepted discovery: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
}
