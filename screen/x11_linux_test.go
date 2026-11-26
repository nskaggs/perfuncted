//go:build linux

package screen

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/composite"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/x11"
	"github.com/nskaggs/perfuncted/v2/internal/x11test"
	"hash/crc32"
	"image"
	"image/color"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestX11BackgroundWindowCaptureReadsTargetPixmapUnderOccluder(t *testing.T) {
	display, stop, err := isolatedBackgroundCaptureXvfb(t)
	if err != nil {
		if os.Getenv("PF_STRICT_BACKGROUND_CAPTURE") == "1" {
			t.Fatal(err)
		}
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
		if os.Getenv("PF_STRICT_BACKGROUND_CAPTURE") == "1" {
			t.Fatal(err)
		}
		t.Skipf("Xvfb Composite extension unavailable: %v", err)
	}
	target := createBackgroundCaptureWindow(t, conn, screenInfo, 10, 10, 64, 48, 0x00ff0000)
	if err := composite.RedirectWindowChecked(conn, target, composite.RedirectManual).Check(); err != nil {
		if os.Getenv("PF_STRICT_BACKGROUND_CAPTURE") == "1" {
			t.Fatal(err)
		}
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

// The X11 reply is synchronous. A caller can revoke image authority while
// the server is responding, and the completed reply must not be published.
func TestX11CaptureRejectsCancellationDuringImageReply(t *testing.T) {
	operations := []struct {
		name string
		run  func(*X11Backend, context.Context) error
	}{
		{"image", func(b *X11Backend, c context.Context) error {
			img, err := b.Grab(c, image.Rect(0, 0, 1, 1))
			if img != nil {
				t.Errorf("canceled image exposed pixels")
			}
			return err
		}},
		{"full hash", func(b *X11Backend, c context.Context) error {
			hash, err := b.GrabFullHash(c)
			if hash != 0 {
				t.Errorf("canceled hash exposed checksum %d", hash)
			}
			return err
		}},
		{"region hash", func(b *X11Backend, c context.Context) error {
			hash, err := b.GrabRegionHash(c, image.Rect(0, 0, 1, 1))
			if hash != 0 {
				t.Errorf("canceled region hash exposed checksum %d", hash)
			}
			return err
		}},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			b, conn := newStubScreenX11Backend(t, false)
			b.screen.WidthInPixels = 1
			b.screen.HeightInPixels = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
				cancel()
				return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Data: []byte{2, 4, 6, 8}}, nil)
			}
			if err := op.run(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled XGetImage returned %v", err)
			}
		})
	}
}

func TestX11HeadlessServerScreenCaptureAndCanonicalRegionHashes(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "no-host-auth")
	if err := os.WriteFile(auth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", auth)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)
	b, err := NewX11Backend(display)
	if err != nil {
		t.Fatalf("open headless screen: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	w, h, err := b.Resolution()
	if err != nil || w != 1024 || h != 768 {
		t.Fatalf("private framebuffer=%dx%d %v", w, h, err)
	}
	rect := image.Rect(3, 5, 11, 13)
	img, err := b.Grab(ctx, rect)
	if err != nil || img == nil || img.Bounds().Dx() != 8 || img.Bounds().Dy() != 8 {
		t.Fatalf("real region=%v %v", img, err)
	}
	hash, err := b.GrabRegionHash(ctx, rect)
	if err != nil || hash == 0 {
		t.Fatalf("region hash %08x %v", hash, err)
	}
	whole, err := b.GrabFullHash(ctx)
	if err != nil || whole == 0 || whole == hash {
		t.Fatalf("full hash %08x ROI %08x %v", whole, hash, err)
	}
	full, err := b.Grab(ctx, image.Rectangle{})
	if err != nil || full.Bounds() != image.Rect(0, 0, 1024, 768) {
		t.Fatalf("full capture=%v %v", full.Bounds(), err)
	}
	if _, err := b.Grab(ctx, image.Rect(32769, 0, 32770, 1)); err == nil {
		t.Fatal("unrepresentable region accepted")
	}
	if _, err := b.Grab(ctx, image.Rect(2000, 2000, 2008, 2008)); err == nil {
		t.Fatal("out-of-screen region accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Grab(canceled, rect); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Grab(ctx, rect); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed backend captured: %v", err)
	}
}

// Only the zero rectangle requests the entire X11 display. An invalid region
// must fail without requesting private pixels from the X server.
func TestX11RejectsNonzeroEmptyRegionsWithoutGetImage(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			b, conn := newStubScreenX11Backend(t, false)
			b.screen.WidthInPixels = 2
			b.screen.HeightInPixels = 2
			called := 0
			conn.GetImageFunc = func(_ byte, _ xproto.Drawable, _, _ int16, _, _ uint16, _ uint32) x11.GetImageCookie {
				called++
				return x11.NewMockGetImageCookie(&xproto.GetImageReply{Data: make([]byte, 16)}, nil)
			}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) hash=%08x err=%v; want invalid region", rect, hash, err)
			}
			if called != 0 {
				t.Fatalf("invalid region dispatched %d XGetImage requests", called)
			}
		})
	}
}

func TestX11CaptureCancellationPrecedesBackendAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{name: "full frame", run: func() error { _, err := (*X11Backend)(nil).GrabFullHash(ctx); return err }},
		{name: "region", run: func() error {
			_, err := (*X11Backend)(nil).GrabRegionHash(ctx, image.Rect(0, 0, 1, 1))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled capture error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestX11CaptureRejectsInvalidGeometryBeforeImageRequest(t *testing.T) {
	t.Run("empty root framebuffer", func(t *testing.T) {
		backend, conn := newStubScreenX11Backend(t, false)
		backend.screen.WidthInPixels = 0
		imageCalls := 0
		conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
			imageCalls++
			return x11.NewMockGetImageCookie(nil, nil)
		}
		_, err := backend.GrabFullHash(context.Background())
		if err == nil || !strings.Contains(err.Error(), "empty capture rectangle") || imageCalls != 0 {
			t.Fatalf("empty root framebuffer returned err=%v image calls=%d", err, imageCalls)
		}
	})

	t.Run("unrepresentable region origin", func(t *testing.T) {
		backend, conn := newStubScreenX11Backend(t, false)
		imageCalls := 0
		conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
			imageCalls++
			return x11.NewMockGetImageCookie(nil, nil)
		}
		_, err := backend.GrabRegionHash(context.Background(), image.Rect(32768, 0, 32769, 1))
		if err == nil || !strings.Contains(err.Error(), "origin out of range") || imageCalls != 0 {
			t.Fatalf("unrepresentable region returned err=%v image calls=%d", err, imageCalls)
		}
	})
}

func TestX11NilBackendLifecycleContracts(t *testing.T) {
	var backend *X11Backend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close() = %v, want nil", err)
	}
	if _, _, err := backend.Resolution(); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend Resolution() error = %v", err)
	}
}

func TestValidateX11PixelBufferRejectsNonPositiveDimensions(t *testing.T) {
	for _, dims := range [][2]int{{0, 1}, {1, 0}, {-1, 2}} {
		if err := validateX11PixelBuffer(nil, dims[0], dims[1]); err == nil {
			t.Errorf("pixel dimensions %dx%d unexpectedly accepted", dims[0], dims[1])
		}
	}
}

// newStubScreenX11Backend creates a test X11Backend backed by the shared
// x11.MockConnection so all screen tests use the same mock infrastructure.
// The mock is returned directly so tests can configure Func hooks without
// type assertions.
func newStubScreenX11Backend(t *testing.T, hasComposite bool) (*X11Backend, *x11.MockConnection) {
	t.Helper()
	screenInfo := &xproto.ScreenInfo{Root: 1, WidthInPixels: 1920, HeightInPixels: 1080}
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return screenInfo }
	// Provide a default GetImage that returns a small non-empty image so tests
	// that don't override it still get a valid (non-error) path.
	mc.GetImageFunc = func(_ byte, _ xproto.Drawable, _, _ int16, w, h uint16, _ uint32) x11.GetImageCookie {
		data := make([]byte, int(w)*int(h)*4)
		for i := range data {
			data[i] = byte(i + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	return &X11Backend{conn: mc, root: 1, screen: screenInfo, hasComposite: hasComposite}, mc
}

func TestX11Backend_Grab(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Errorf("Grab() image size = %dx%d, want 2x2", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11BackendRejectsUninitializedState(t *testing.T) {
	b := &X11Backend{}

	if _, _, err := b.Resolution(); err == nil {
		t.Fatal("Resolution on an uninitialized backend succeeded")
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil {
		t.Fatal("Grab on an uninitialized backend succeeded")
	}
	if _, err := b.GrabFullHash(context.Background()); err == nil {
		t.Fatal("GrabFullHash on an uninitialized backend succeeded")
	}
	if _, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1)); err == nil {
		t.Fatal("GrabRegionHash on an uninitialized backend succeeded")
	}
}

func TestX11Backend_Grab_EmptyRect(t *testing.T) { //nolint:dupl
	b, mc := newStubScreenX11Backend(t, false)
	// Empty rect should grab full screen - set up mock to return full screen data
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		// Create data for full screen (1920x1080)
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 0, 0)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Errorf("Grab() with empty rect should grab full screen, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11Backend_Grab_WithComposite(t *testing.T) {
	var pixmapCreated bool
	b, mc := newStubScreenX11Backend(t, true)
	mc.NewIDFunc = func() (uint32, error) {
		return 100, nil
	}
	mc.NameWindowPixmapFunc = func(w xproto.Window, p xproto.Pixmap) x11.NameWindowPixmapCookie {
		pixmapCreated = true
		return &x11.MockCheckCookie{}
	}
	// Set up GetImage to return data for 100x100 image
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 100, 100)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("Grab() returned nil image")
	}
	if !pixmapCreated {
		t.Error("Grab() with composite should have created a pixmap via NameWindowPixmap")
	}
}

func TestX11Backend_Grab_WithoutComposite(t *testing.T) { //nolint:dupl
	b, mc := newStubScreenX11Backend(t, false)
	// Set up GetImage to return data for 100x100 image
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(10, 20, 110, 120)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() != 100 || img.Bounds().Dy() != 100 {
		t.Errorf("Grab() image size = %dx%d, want 100x100", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11Backend_Grab_Error(t *testing.T) {
	b, mc := newStubScreenX11Backend(t, false)
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
	}
	rect := image.Rect(0, 0, 100, 100)
	_, err := b.Grab(context.Background(), rect)
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("Grab() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_GrabFullHash(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b, mc := newStubScreenX11Backend(t, false)
	b.screen.WidthInPixels = 2
	b.screen.HeightInPixels = 1
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	hash, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabFullHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabFullHash_WithComposite(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b, mc := newStubScreenX11Backend(t, true)
	b.screen.WidthInPixels = 2
	b.screen.HeightInPixels = 1
	mc.NewIDFunc = func() (uint32, error) {
		return 100, nil
	}
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	hash, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabFullHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabRegionHash(t *testing.T) {
	data := []byte{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	}
	expectedHash := crc32.ChecksumIEEE(data)

	b, mc := newStubScreenX11Backend(t, false)
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 2, 2)
	hash, err := b.GrabRegionHash(context.Background(), rect)
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabRegionHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabRegionHash_EmptyRect(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	hash1, _ := b.GrabFullHash(context.Background())
	hash2, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 0, 0))
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("GrabRegionHash(empty rect) = %d, want %d (same as GrabFullHash)", hash2, hash1)
	}
}

func TestX11Backend_GrabRegionHash_WithComposite(t *testing.T) {
	data := []byte{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	}
	expectedHash := crc32.ChecksumIEEE(data)

	b, mc := newStubScreenX11Backend(t, true)
	mc.NewIDFunc = func() (uint32, error) {
		return 100, nil
	}
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 2, 2)
	hash, err := b.GrabRegionHash(context.Background(), rect)
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabRegionHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_New_NoDisplay(t *testing.T) {
	orig := os.Getenv("DISPLAY")
	os.Unsetenv("DISPLAY")
	defer os.Setenv("DISPLAY", orig)
	_, err := NewX11Backend("")
	if err == nil || !strings.Contains(err.Error(), "screen/x11: connect to display") {
		t.Fatalf("NewX11Backend() error = %v, want connection error", err)
	}
}

func TestX11Backend_Close(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	if err := b.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

type trackingX11Connection struct {
	*x11.MockConnection
	closeOnce func()
	closed    chan struct{}
}

func (c *trackingX11Connection) Close() {
	c.closeOnce()
}

type blockedGetImageCookie struct {
	started chan struct{}
	closed  <-chan struct{}
}

func (c *blockedGetImageCookie) Reply() (*xproto.GetImageReply, error) {
	close(c.started)
	<-c.closed
	return nil, net.ErrClosed
}

func TestX11BackendCloseUnblocksBlockedReply(t *testing.T) {
	mock := &x11.MockConnection{}
	conn := &trackingX11Connection{
		MockConnection: mock,
		closed:         make(chan struct{}),
	}
	conn.closeOnce = sync.OnceFunc(func() { close(conn.closed) })
	defer conn.Close()
	b := &X11Backend{
		conn:   conn,
		root:   1,
		screen: &xproto.ScreenInfo{Root: 1, WidthInPixels: 1, HeightInPixels: 1},
	}
	replyStarted := make(chan struct{})
	mock.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
		return &blockedGetImageCookie{started: replyStarted, closed: conn.closed}
	}

	grabDone := make(chan error, 1)
	go func() {
		_, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1))
		grabDone <- err
	}()
	<-replyStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock the blocked Reply")
	}

	select {
	case err := <-grabDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Grab error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Grab did not exit after Close")
	}
	if _, _, err := b.Resolution(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Resolution after Close error = %v, want net.ErrClosed", err)
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Grab after Close error = %v, want net.ErrClosed", err)
	}
}

func TestX11Backend_Grab_ColorCheck(t *testing.T) {
	data := []byte{1, 2, 3, 255}
	b, mc := newStubScreenX11Backend(t, false)
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 1, 1)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	c := rgba.RGBAAt(0, 0)
	if c.R != 3 || c.G != 2 || c.B != 1 || c.A != 255 {
		t.Errorf("Grab() pixel = RGBA(%d,%d,%d,%d), want RGBA(3,2,1,255)", c.R, c.G, c.B, c.A)
	}
}

func TestX11Backend_Resolution(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	w, h, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution() unexpected error: %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Errorf("Resolution() = %dx%d, want 1920x1080", w, h)
	}
}

func TestX11Backend_ImplementsScreenshotter(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	var _ Screenshotter = b
}

func TestX11Backend_ImplementsResolver(t *testing.T) {
	b, _ := newStubScreenX11Backend(t, false)
	var _ Resolver = b
}

func TestDecodeBGRA_Integration(t *testing.T) {
	b, mc := newStubScreenX11Backend(t, false)
	data := []byte{
		10, 20, 30, 255,
		40, 50, 60, 255,
		70, 80, 90, 255,
		100, 110, 120, 255,
	}
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	tests := []struct {
		x, y    int
		r, g, b uint8
	}{
		{0, 0, 30, 20, 10},
		{1, 0, 60, 50, 40},
		{0, 1, 90, 80, 70},
		{1, 1, 120, 110, 100},
	}
	for _, tc := range tests {
		c := rgba.RGBAAt(tc.x, tc.y)
		if c.R != tc.r || c.G != tc.g || c.B != tc.b {
			t.Errorf("(%d,%d): got RGB(%d,%d,%d) want RGB(%d,%d,%d)",
				tc.x, tc.y, c.R, c.G, c.B, tc.r, tc.g, tc.b)
		}
	}
}

func TestX11Backend_GrabFullHash_Error(t *testing.T) {
	b, mc := newStubScreenX11Backend(t, false)
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
	}
	_, err := b.GrabFullHash(context.Background())
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("GrabFullHash() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_GrabRegionHash_Error(t *testing.T) {
	b, mc := newStubScreenX11Backend(t, false)
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
	}
	rect := image.Rect(0, 0, 100, 100)
	_, err := b.GrabRegionHash(context.Background(), rect)
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("GrabRegionHash() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_Grab_ImageCheck(t *testing.T) {
	b, mc := newStubScreenX11Backend(t, false)
	data := make([]byte, 4*4*4)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			off := (y*2 + x) * 4
			data[off] = byte(x * 10)
			data[off+1] = byte(y * 10)
			data[off+2] = byte(x * y * 10)
			data[off+3] = 255
		}
	}
	mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("Grab() returned nil image")
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	if rgba.Bounds().Dx() != 2 || rgba.Bounds().Dy() != 2 {
		t.Errorf("Grab() image size = %dx%d, want 2x2", rgba.Bounds().Dx(), rgba.Bounds().Dy())
	}
	c := rgba.RGBAAt(1, 1)
	if c.R != 10 || c.G != 10 || c.B != 10 {
		t.Errorf("Grab() pixel (1,1) = RGB(%d,%d,%d), want RGB(10,10,10)", c.R, c.G, c.B)
	}
}

func TestX11Backend_ShortPixelBufferErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*X11Backend) error
	}{
		{
			name: "Grab",
			run: func(b *X11Backend) error {
				_, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1))
				return err
			},
		},
		{
			name: "GrabFullHash",
			run: func(b *X11Backend) error {
				_, err := b.GrabFullHash(context.Background())
				return err
			},
		},
		{
			name: "GrabRegionHash",
			run: func(b *X11Backend) error {
				_, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, mc := newStubScreenX11Backend(t, false)
			mc.GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
				return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: []byte{1, 2, 3}}, nil)
			}
			if tc.name == "GrabFullHash" {
				b.screen.WidthInPixels = 1
				b.screen.HeightInPixels = 1
			}
			err := tc.run(b)
			if err == nil || !strings.Contains(err.Error(), "short pixel buffer") {
				t.Fatalf("%s() error = %v, want short pixel buffer error", tc.name, err)
			}
		})
	}
}

func TestValidateX11RectRejectsWireTruncation(t *testing.T) {
	tests := []image.Rectangle{
		image.Rect(32768, 0, 32769, 1),
		image.Rect(0, 0, 65536, 1),
		image.Rect(0, 0, 1, 65536),
	}
	for _, rect := range tests {
		if err := validateX11Rect(rect); err == nil {
			t.Errorf("validateX11Rect(%v) unexpectedly accepted", rect)
		}
	}
	if err := validateX11Rect(image.Rect(-32768, -32768, 32767, 32767)); err != nil {
		t.Fatalf("valid X11 rectangle rejected: %v", err)
	}
}
