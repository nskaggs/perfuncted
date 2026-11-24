//go:build linux

package screen

import (
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestX11BackgroundWindowCaptureReadsTargetPixmapUnderOccluder(t *testing.T) {
	display, stop, err := isolatedBackgroundCaptureXvfb(t)
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	defer stop()

	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	screenInfo := xproto.Setup(conn).DefaultScreen(conn)
	if err := composite.Init(conn); err != nil {
		t.Skipf("Xvfb Composite extension unavailable: %v", err)
	}
	target := createBackgroundCaptureWindow(t, conn, screenInfo, 10, 10, 64, 48, 0x00ff0000)
	if err := composite.RedirectWindowChecked(conn, target, composite.RedirectManual).Check(); err != nil {
		t.Skipf("Xvfb cannot redirect a test-owned target window: %v", err)
	}

	backend, err := NewX11Backend(display)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	unredirected := createBackgroundCaptureWindow(t, conn, screenInfo, 90, 90, 16, 16, 0x000000ff)
	if _, err := backend.CaptureWindowPixmap(context.Background(), strconv.FormatUint(uint64(unredirected), 10)); !errors.Is(err, ErrBackgroundWindowCaptureUnsupported) {
		t.Fatalf("unredirected target error=%v, want explicit unsupported", err)
	}
	id := strconv.FormatUint(uint64(target), 10)
	first, err := backend.CaptureWindowPixmap(context.Background(), id)
	if err != nil {
		t.Fatalf("capture unobscured target: %v", err)
	}
	if first.Visibility != WindowPixmapUnobscured {
		t.Fatalf("unobscured target visibility=%q", first.Visibility)
	}
	if first.ForegroundKnown {
		t.Fatal("Xvfb without a window manager reported a known active window")
	}
	assertPixelColor(t, first.Image.At(2, 2), color.RGBA{R: 0xff, A: 0xff})

	_ = createBackgroundCaptureWindow(t, conn, screenInfo, 24, 20, 24, 20, 0x0000ff00)
	conn.Sync()
	second, err := backend.CaptureWindowPixmap(context.Background(), id)
	if err != nil {
		t.Fatalf("capture target under mapped occluder: %v", err)
	}
	if second.Visibility != WindowPixmapOccluded {
		t.Fatalf("overlapped target visibility=%q, want occluded", second.Visibility)
	}
	assertPixelColor(t, second.Image.At(20, 16), color.RGBA{R: 0xff, A: 0xff})
	if second.Image.Bounds().Dx() != 64 || second.Image.Bounds().Dy() != 48 {
		t.Fatalf("target pixmap bounds=%v", second.Image.Bounds())
	}

	if err := xproto.UnmapWindowChecked(conn, target).Check(); err != nil {
		t.Fatal(err)
	}
	conn.Sync()
	if _, err := backend.CaptureWindowPixmap(context.Background(), id); !errors.Is(err, ErrBackgroundWindowCaptureUnsupported) {
		t.Fatalf("unmapped target error=%v, want explicit unsupported", err)
	}
}

func isolatedBackgroundCaptureXvfb(t *testing.T) (string, func(), error) {
	t.Helper()
	auth := filepath.Join(t.TempDir(), "no-host-auth")
	if err := os.WriteFile(auth, nil, 0600); err != nil {
		return "", nil, err
	}
	t.Setenv("XAUTHORITY", auth)
	return x11test.StartXvfb()
}

func createBackgroundCaptureWindow(t *testing.T, conn *xgb.Conn, screenInfo *xproto.ScreenInfo, x, y int16, width, height uint16, pixel uint32) xproto.Window {
	t.Helper()
	id, err := xproto.NewWindowId(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateWindowChecked(conn, screenInfo.RootDepth, id, screenInfo.Root,
		x, y, width, height, 0, xproto.WindowClassInputOutput, screenInfo.RootVisual,
		xproto.CwBackPixel, []uint32{pixel}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := xproto.MapWindowChecked(conn, id).Check(); err != nil {
		t.Fatal(err)
	}
	conn.Sync()
	return id
}

func assertPixelColor(t *testing.T, got color.Color, want color.RGBA) {
	t.Helper()
	r, g, b, a := got.RGBA()
	if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != want.A {
		t.Fatalf("pixel=%#04x%#04x%#04x%#04x, want %#v", r>>8, g>>8, b>>8, a>>8, want)
	}
}
