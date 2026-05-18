package perfuncted_test

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/pftest"
)

// ── DetectSession ─────────────────────────────────────────────────────────────

func TestDetectSession(t *testing.T) {
	// In the test environment, XDG_RUNTIME_DIR should not look like a nested
	// perfuncted session, so DetectSession should return "host".
	kind, details := perfuncted.DetectSession()
	if kind != "host" {
		t.Fatalf("DetectSession: got kind=%q, want %q (details=%v)", kind, "host", details)
	}
	if _, ok := details["current_xdg"]; !ok {
		t.Error("DetectSession host: expected 'current_xdg' key in details")
	}
}

func TestDetectSession_Nested(t *testing.T) {
	fakeXDG := filepath.Join(os.TempDir(), "perfuncted-xdg-test-nested")
	t.Setenv("XDG_RUNTIME_DIR", fakeXDG)
	t.Setenv("WAYLAND_DISPLAY", "wayland-99")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/test/bus")

	kind, details := perfuncted.DetectSession()
	if kind != "nested" {
		t.Fatalf("DetectSession: got kind=%q, want %q", kind, "nested")
	}
	if details["dir"] != fakeXDG {
		t.Errorf("details[dir]=%q, want %q", details["dir"], fakeXDG)
	}
	if details["wayland_display"] != "wayland-99" {
		t.Errorf("details[wayland_display]=%q, want %q", details["wayland_display"], "wayland-99")
	}
}

// ── ScreenBundle helpers ──────────────────────────────────────────────────────

// newTestPF creates a Perfuncted with a mock screenshotter backed by a solid
// 100×100 red image.
func newTestPF(sc *pftest.Screenshotter) *perfuncted.Perfuncted {
	return pftest.New(sc, nil, nil, nil)
}

func solidRedImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: 255, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// ── ScreenBundle tests ─────────────────────────────────────────────────────────

func TestScreenBundle_GetAllPixels(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 10, Height: 10}
	pf := newTestPF(sc)
	defer pf.Close()

	img, err := pf.Screen.GetAllPixels(context.Background())
	if err != nil {
		t.Fatalf("GetAllPixels: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 10 || b.Dy() != 10 {
		t.Fatalf("GetAllPixels: got %dx%d, want 10x10", b.Dx(), b.Dy())
	}
}

func TestScreenBundle_GrabRegion(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 20, Height: 20}
	pf := newTestPF(sc)
	defer pf.Close()

	rect := image.Rect(0, 0, 10, 10)
	img, err := pf.Screen.GrabRegion(context.Background(), rect)
	if err != nil {
		t.Fatalf("GrabRegion: %v", err)
	}
	if img == nil {
		t.Fatal("GrabRegion: returned nil image")
	}
}

func TestScreenBundle_GetPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	want := color.RGBA{R: 12, G: 34, B: 56, A: 255}
	img.SetRGBA(3, 4, want)
	sc := &pftest.Screenshotter{Frames: []image.Image{img}, ZeroOrigin: true}
	pf := newTestPF(sc)
	defer pf.Close()

	got, err := pf.Screen.GetPixel(context.Background(), 3, 4)
	if err != nil {
		t.Fatalf("GetPixel: %v", err)
	}
	if got != want {
		t.Fatalf("GetPixel: got %#v, want %#v", got, want)
	}
}

func TestScreenBundle_GetMultiplePixelsTranslatesBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 30))
	first := color.RGBA{R: 10, A: 255}
	second := color.RGBA{G: 20, A: 255}
	img.SetRGBA(11, 22, first)
	img.SetRGBA(16, 26, second)
	sc := &pftest.Screenshotter{Frames: []image.Image{img}, ZeroOrigin: true}
	pf := newTestPF(sc)
	defer pf.Close()

	got, err := pf.Screen.GetMultiplePixels(context.Background(), []image.Point{
		{X: 11, Y: 22},
		{X: 16, Y: 26},
	})
	if err != nil {
		t.Fatalf("GetMultiplePixels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMultiplePixels len = %d, want 2", len(got))
	}
	if got[0] != first || got[1] != second {
		t.Fatalf("GetMultiplePixels = %#v, want %#v/%#v", got, first, second)
	}
}

func TestScreenBundle_GetMultiplePixelsEmpty(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 4, Height: 4}
	pf := newTestPF(sc)
	defer pf.Close()

	got, err := pf.Screen.GetMultiplePixels(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetMultiplePixels(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetMultiplePixels(nil) len = %d, want 0", len(got))
	}
}

func TestScreenBundle_CaptureRegion(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 8, Height: 8}
	pf := newTestPF(sc)
	defer pf.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "capture.png")

	if err := pf.Screen.CaptureRegion(context.Background(), image.Rect(0, 0, 8, 8), path); err != nil {
		t.Fatalf("CaptureRegion: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CaptureRegion: output file not created: %v", err)
	}
}

func TestScreenBundle_WaitForFn(t *testing.T) {
	red := solidRedImage(4, 4)
	sc := &pftest.Screenshotter{Frames: []image.Image{red}}
	pf := newTestPF(sc)
	defer pf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Predicate: always true → returns on first grab.
	img, err := pf.Screen.WaitForFn(ctx, image.Rect(0, 0, 4, 4), func(_ context.Context, _ image.Image) bool {
		return true
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForFn: %v", err)
	}
	if img == nil {
		t.Fatal("WaitForFn: returned nil image")
	}
}

func TestScreenBundle_WaitForNoChange(t *testing.T) {
	red := solidRedImage(4, 4)
	// Two identical frames → stable=2 satisfied immediately.
	sc := &pftest.Screenshotter{Frames: []image.Image{red, red, red}}
	pf := newTestPF(sc)
	defer pf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForNoChange(ctx, image.Rect(0, 0, 4, 4), 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForNoChange: %v", err)
	}
	wantHash := find.PixelHash(red, nil)
	if h != wantHash {
		t.Fatalf("WaitForNoChange: got hash %08x, want %08x", h, wantHash)
	}
}

func TestScreenBundle_WaitForNoChangeFrom(t *testing.T) {
	red := solidRedImage(4, 4)
	sc := &pftest.Screenshotter{Frames: []image.Image{red}}
	pf := newTestPF(sc)
	defer pf.Close()

	wantHash := find.PixelHash(red, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// initial=wantHash, stable=1 → immediately satisfied on first grab.
	h, err := pf.Screen.WaitForNoChangeFrom(ctx, image.Rect(0, 0, 4, 4), wantHash, 1, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForNoChangeFrom: %v", err)
	}
	if h != wantHash {
		t.Fatalf("WaitForNoChangeFrom: got hash %08x, want %08x", h, wantHash)
	}
}

func TestScreenBundle_WaitForSettleNilAction(t *testing.T) {
	red := solidRedImage(4, 4)
	blue := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			blue.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
		}
	}

	sc := &pftest.Screenshotter{Frames: []image.Image{red, blue, blue, blue}}
	pf := newTestPF(sc)
	defer pf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForSettle(ctx, image.Rect(0, 0, 4, 4), nil, 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForSettle(nil): %v", err)
	}
	if h == 0 {
		t.Fatal("WaitForSettle(nil): returned zero hash")
	}
}

func TestScreenBundle_FindColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Place a blue pixel at (5, 5).
	img.SetRGBA(5, 5, color.RGBA{B: 255, A: 255})
	sc := &pftest.Screenshotter{Frames: []image.Image{img}}
	pf := newTestPF(sc)
	defer pf.Close()

	pt, err := pf.Screen.FindColor(context.Background(), image.Rect(0, 0, 10, 10), color.RGBA{B: 255, A: 255}, 0)
	if err != nil {
		t.Fatalf("FindColor: %v", err)
	}
	if pt.X != 5 || pt.Y != 5 {
		t.Fatalf("FindColor: got (%d,%d), want (5,5)", pt.X, pt.Y)
	}
}

func TestScreenBundle_WaitForChange(t *testing.T) {
	red := solidRedImage(4, 4)
	blue := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			blue.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	initial := find.PixelHash(red, nil)

	// First grab returns blue (already changed).
	sc := &pftest.Screenshotter{Frames: []image.Image{blue}}
	pf := newTestPF(sc)
	defer pf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := pf.Screen.WaitForChange(ctx, image.Rect(0, 0, 4, 4), initial, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForChange: %v", err)
	}
	if got == initial {
		t.Fatal("WaitForChange: returned same hash as initial")
	}
}

func TestScreenBundle_WaitFor(t *testing.T) {
	red := solidRedImage(4, 4)
	want := find.PixelHash(red, nil)
	sc := &pftest.Screenshotter{Frames: []image.Image{red}}
	pf := newTestPF(sc)
	defer pf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := pf.Screen.WaitFor(ctx, image.Rect(0, 0, 4, 4), want, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitFor: %v", err)
	}
	if got != want {
		t.Fatalf("WaitFor: got hash %08x, want %08x", got, want)
	}
}

func TestScreenBundle_ScanFor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	// Unique pattern in top-left quadrant.
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	sc := &pftest.Screenshotter{Frames: []image.Image{img}}
	pf := newTestPF(sc)
	defer pf.Close()

	rect := image.Rect(0, 0, 10, 10)
	want := find.PixelHash(img.SubImage(rect), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := pf.Screen.ScanFor(ctx, []image.Rectangle{rect}, []uint32{want}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("ScanFor: %v", err)
	}
	if result.Hash != want {
		t.Fatalf("ScanFor: got hash %08x, want %08x", result.Hash, want)
	}
}

func TestScreenBundle_Resolution(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1920, Height: 1080}
	pf := newTestPF(sc)
	defer pf.Close()

	w, h, err := pf.Screen.Resolution(context.Background())
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Fatalf("Resolution: got %dx%d, want 1920x1080", w, h)
	}
}
