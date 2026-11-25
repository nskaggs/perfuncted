package perfuncted_test

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clipboardSpy struct {
	text       string
	getCalls   int
	setCalls   int
	closeCalls int
	ctxValue   any
	getErr     error
	setErr     error
	closeErr   error
}

func (c *clipboardSpy) Get(ctx context.Context) (string, error) {
	c.getCalls++
	c.ctxValue = ctx.Value(contextKey{})
	return c.text, c.getErr
}

func (c *clipboardSpy) Set(ctx context.Context, text string) error {
	c.setCalls++
	c.ctxValue = ctx.Value(contextKey{})
	c.text = text
	return c.setErr
}

func (c *clipboardSpy) Close() error {
	c.closeCalls++
	return c.closeErr
}

func TestClipboardBundle(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(sc, inp, nil, cb)

	t.Run("Clipboard", func(t *testing.T) {
		ctx := context.Background()
		if err := pf.Clipboard.Set(ctx, "hello"); err != nil {
			t.Fatal(err)
		}
		if got, err := pf.Clipboard.Get(ctx); err != nil || got != "hello" {
			t.Fatal(err, got)
		}
		if err := pf.Paste(ctx, "world"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPerfunctedPaste(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)
	if err := pf.Paste(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if cb.Text != "hello" {
		t.Fatalf("Clipboard.Text = %q; want %q", cb.Text, "hello")
	}
	want := []string{"type:{ctrl+v}"}
	if len(inp.Calls) < len(want) {
		t.Fatalf("calls = %v", inp.Calls)
	}
	for i := range want {
		if inp.Calls[i] != want[i] {
			t.Errorf("call[%d] = %q; want %q", i, inp.Calls[i], want[i])
		}
	}
}

func TestClipboardBundleContextErrorsAndClose(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextKey{}, "clipboard-token")
	closeErr := errors.New("clipboard close failed")
	cb := &clipboardSpy{closeErr: closeErr}
	pf := pftest.New(nil, nil, nil, cb)

	if err := pf.Clipboard.Set(ctx, "hello"); err != nil {
		t.Fatalf("Clipboard.Set: %v", err)
	}
	if cb.setCalls != 1 {
		t.Fatalf("Set calls = %d, want 1", cb.setCalls)
	}
	if cb.ctxValue != "clipboard-token" {
		t.Fatalf("Set context value = %v, want clipboard-token", cb.ctxValue)
	}

	got, err := pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("Clipboard.Get: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Clipboard.Get = %q, want %q", got, "hello")
	}
	if cb.getCalls != 1 {
		t.Fatalf("Get calls = %d, want 1", cb.getCalls)
	}

	if err := pf.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
	if cb.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", cb.closeCalls)
	}
}

func TestPerfunctedPasteFallsBackToInputWhenClipboardUnavailable(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Paste(context.Background(), "typed fallback"); err != nil {
		t.Fatalf("Paste fallback: %v", err)
	}
	if got := inp.Typed(); got != "typed fallback" {
		t.Fatalf("Paste fallback typed = %q, want %q", got, "typed fallback")
	}
}

func TestPerfunctedPasteDoesNotModifyClipboardWithoutInput(t *testing.T) {
	cb := &pftest.Clipboard{Text: "original"}
	pf := pftest.New(nil, nil, nil, cb)

	err := pf.Paste(context.Background(), "replacement")
	if !errors.Is(err, perfuncted.ErrUnavailable) {
		t.Fatalf("Paste error = %v, want unavailable", err)
	}
	if cb.Text != "original" {
		t.Fatalf("clipboard text = %q after failed paste, want original", cb.Text)
	}
}

type outputSpyLister struct {
	listCalls  int
	closeCalls int
	ctxValue   any
	infos      []output.Info
	err        error
	closeErr   error
}

func (o *outputSpyLister) List(ctx context.Context) ([]output.Info, error) {
	o.listCalls++
	o.ctxValue = ctx.Value(contextKey{})
	return o.infos, o.err
}

func (o *outputSpyLister) Close() error {
	o.closeCalls++
	return o.closeErr
}

func TestOutputBundleListAndClose(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), contextKey{}, "token")
	closeErr := errors.New("output close failed")
	spy := &outputSpyLister{
		infos: []output.Info{{
			Name:        "HDMI-A-1",
			Backend:     "wayland",
			Geometry:    output.Geometry{X: 0, Y: 0, W: 1920, H: 1080},
			ResolutionW: 1920,
			ResolutionH: 1080,
			Scale:       2,
		}},
		closeErr: closeErr,
	}

	pf := pftest.NewWithOutputs(nil, nil, nil, spy, nil)

	got, err := pf.Outputs.List(ctx)
	if err != nil {
		t.Fatalf("Output.List: %v", err)
	}
	if spy.listCalls != 1 {
		t.Fatalf("List calls = %d, want 1", spy.listCalls)
	}
	if spy.closeCalls != 0 {
		t.Fatalf("Close calls after List = %d, want 0", spy.closeCalls)
	}
	if spy.ctxValue != "token" {
		t.Fatalf("List context value = %v, want token", spy.ctxValue)
	}
	if len(got) != 1 {
		t.Fatalf("Output.List len = %d, want 1", len(got))
	}
	if got[0].Name != "HDMI-A-1" || got[0].Backend != "wayland" {
		t.Fatalf("Output.List returned %+v, want HDMI-A-1/wayland", got[0])
	}
	if got[0].Geometry.W != 1920 || got[0].Geometry.H != 1080 {
		t.Fatalf("Output.List geometry = %+v, want 1920x1080", got[0].Geometry)
	}

	if err := pf.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
	if spy.closeCalls != 1 {
		t.Fatalf("Close calls after Perfuncted.Close = %d, want 1", spy.closeCalls)
	}
}

// ── DetectSession ─────────────────────────────────────────────────────────────

func TestDetectSession(t *testing.T) {
	// In the test environment, XDG_RUNTIME_DIR should not look like a nested
	// perfuncted session, so DetectSession should return "host".
	detection := perfuncted.DetectSession()
	if detection.Kind != perfuncted.TargetHost {
		t.Fatalf("DetectSession: got kind=%q, want %q", detection.Kind, perfuncted.TargetHost)
	}
}

func TestDetectSession_Nested(t *testing.T) {
	fakeXDG := filepath.Join(os.TempDir(), "perfuncted-xdg-test-nested")
	t.Setenv("XDG_RUNTIME_DIR", fakeXDG)
	t.Setenv("WAYLAND_DISPLAY", "wayland-99")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/test/bus")

	detection := perfuncted.DetectSession()
	if detection.Kind != perfuncted.TargetNested {
		t.Fatalf("DetectSession: got kind=%q, want %q", detection.Kind, perfuncted.TargetNested)
	}
	if detection.XDGRuntimeDir != fakeXDG {
		t.Errorf("XDGRuntimeDir=%q, want %q", detection.XDGRuntimeDir, fakeXDG)
	}
	if detection.WaylandDisplay != "wayland-99" {
		t.Errorf("WaylandDisplay=%q, want %q", detection.WaylandDisplay, "wayland-99")
	}
}

// ── ScreenBundle helpers ──────────────────────────────────────────────────────

// newTestPF creates a Perfuncted with a mock screenshotter backed by a solid
// 100×100 red image.
func newTestPF(sc *pftest.Screenshotter) *perfuncted.Session {
	return pftest.New(sc, nil, nil, nil)
}

func solidRedImage(w, h int) *image.RGBA { //nolint:unparam
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: 255, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

type oversizedImage struct{}

func (oversizedImage) ColorModel() color.Model { return color.RGBAModel }

func (oversizedImage) Bounds() image.Rectangle { return image.Rect(0, 0, 1<<32, 1) }

func (oversizedImage) At(int, int) color.Color { return color.RGBA{} }

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

func TestScreenBundle_GetPixelRejectsEmptyCapture(t *testing.T) {
	sc := &pftest.Screenshotter{
		Frames:     []image.Image{image.NewRGBA(image.Rectangle{})},
		ZeroOrigin: true,
	}
	p := newTestPF(sc)
	defer p.Close()

	if _, err := p.Screen.GetPixel(context.Background(), 5, 5); err == nil {
		t.Fatal("GetPixel succeeded with an empty capture")
	}
}

func TestScreenBundle_GetMultiplePixelsRejectsMissingPoint(t *testing.T) {
	sc := &pftest.Screenshotter{
		Frames:     []image.Image{image.NewRGBA(image.Rect(0, 0, 1, 1))},
		ZeroOrigin: true,
	}
	p := newTestPF(sc)
	defer p.Close()

	if _, err := p.Screen.GetMultiplePixels(context.Background(), []image.Point{{X: 5, Y: 5}}); err == nil {
		t.Fatal("GetMultiplePixels succeeded without a captured requested point")
	}
}

func TestScreenBundle_GetPixelRejectsCoordinateOverflow(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 2, Height: 2}
	p := newTestPF(sc)
	defer p.Close()

	if _, err := p.Screen.GetPixel(context.Background(), math.MaxInt, 0); err == nil {
		t.Fatal("GetPixel succeeded with an overflowing coordinate")
	}
}

func TestScreenBundle_GetMultiplePixelsRejectsCoordinateOverflow(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 2, Height: 2}
	p := newTestPF(sc)
	defer p.Close()

	if _, err := p.Screen.GetMultiplePixels(context.Background(), []image.Point{{X: math.MaxInt, Y: 0}}); err == nil {
		t.Fatal("GetMultiplePixels succeeded with an overflowing coordinate")
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

func TestScreenBundle_CaptureRegionWrapsFileErrors(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 2, Height: 2}
	p := newTestPF(sc)
	defer p.Close()

	var operationErr *perfuncted.OperationError
	err := p.Screen.CaptureRegion(
		context.Background(),
		image.Rect(0, 0, 2, 2),
		t.TempDir(),
	)
	if !errors.As(err, &operationErr) {
		t.Fatalf("CaptureRegion error = %v, want OperationError", err)
	}
	if !errors.Is(err, perfuncted.ErrOperationFailed) {
		t.Fatalf("CaptureRegion error = %v, want ErrOperationFailed", err)
	}
	if operationErr.Operation != "capture" {
		t.Fatalf("CaptureRegion operation = %q, want capture", operationErr.Operation)
	}
}

func TestScreenBundle_CaptureRegionPreservesDestinationOnEncodeFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := os.WriteFile(path, []byte("existing capture"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}
	sc := &pftest.Screenshotter{Frames: []image.Image{oversizedImage{}}}
	p := newTestPF(sc)
	defer p.Close()

	if err := p.Screen.CaptureRegion(context.Background(), image.Rectangle{}, path); err == nil {
		t.Fatal("CaptureRegion succeeded for an oversized image")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != "existing capture" {
		t.Fatalf("destination = %q, want existing capture preserved", got)
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

func TestScreenBundle_GetMultiplePixelsMapsAcrossOutputScale(t *testing.T) {
	const (
		scale  = 2
		region = 10
		scaled = region * scale
	)
	bgColor := color.RGBA{R: 1, G: 2, B: 3, A: 255}
	want := color.RGBA{R: 200, G: 100, B: 50, A: 255}

	// The backend returns a capture at twice the requested region's scale, which
	// is what a scaled output does. Every pixel that is not one of the expected
	// positions is a decoy, so a wrong lookup cannot pass.
	capture := image.NewRGBA(image.Rect(0, 0, scaled, scaled))
	draw.Draw(capture, capture.Bounds(), &image.Uniform{C: bgColor}, image.Point{}, draw.Src)
	points := []image.Point{{0, 0}, {5, 5}, {9, 9}}
	for _, point := range points {
		capture.SetRGBA(point.X*scale, point.Y*scale, want)
	}

	sc := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { return capture, nil }}
	pf := newTestPF(sc)
	defer pf.Close()

	got, err := pf.Screen.GetMultiplePixels(context.Background(), points)
	if err != nil {
		t.Fatalf("GetMultiplePixels: %v", err)
	}
	if len(got) != len(points) {
		t.Fatalf("GetMultiplePixels len = %d, want %d", len(got), len(points))
	}
	for i, gotColor := range got {
		if gotColor != want {
			t.Fatalf(
				"desktop point %v (image pixel %d,%d) = %#v, want %#v",
				points[i], points[i].X*scale, points[i].Y*scale, gotColor, want,
			)
		}
	}
}

// A capture clipped by the same proportion on both edges keeps its aspect ratio, so
// a uniform-scale check alone accepts it, and a point past the midpoint reads a
// different place on screen than the one requested.
func TestScreenBundleRejectsAProportionallyClippedCapture(t *testing.T) {
	pf := newTestPF(&pftest.Screenshotter{
		GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
			// Half of the 100x50 region these points span: proportional, so the axes
			// still agree at 2:1.
			capture := image.NewRGBA(image.Rect(0, 0, 50, 25))
			draw.Draw(capture, capture.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
			return capture, nil
		},
	})
	defer pf.Close()

	points := []image.Point{{0, 0}, {99, 49}}
	got, err := pf.Screen.GetMultiplePixels(context.Background(), points)
	if err == nil {
		t.Fatalf("GetMultiplePixels returned %v for a capture smaller than the region", got)
	}
	if !strings.Contains(err.Error(), "clipped") {
		t.Fatalf("error = %v, want it to report the capture as clipped", err)
	}
}

func TestNewAssemblesAllBackends(t *testing.T) {
	t.Parallel()
	// Use pftest to provide mocks for all backends.
	sc := &pftest.Screenshotter{Width: 100, Height: 100}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	cb := &pftest.Clipboard{}
	ctx := context.Background()

	pf := pftest.New(sc, inp, mgr, cb)
	defer pf.Close()

	if _, err := pf.Screen.Grab(
		ctx,
		image.Rect(0, 0, 1, 1),
	); err != nil {
		t.Errorf("Screen.Grab: %v", err)
	}
	// pf.Input uses InputBundle which wraps inp.
	if err := pf.Input.Type(ctx, "a"); err != nil {
		t.Errorf("Type: %v", err)
	}
	if err := pf.Input.Type(ctx, "^c"); err != nil {
		t.Errorf("Type ctrl+c: %v", err)
	}
	if _, err := pf.Windows.List(ctx, perfuncted.WindowMatch{}); err != nil {
		t.Errorf("Windows.List: %v", err)
	}
	if err := pf.Clipboard.Set(ctx, "assembled"); err != nil {
		t.Errorf("Clipboard.Set: %v", err)
	}
	if got, err := pf.Clipboard.Get(ctx); err != nil || got != "assembled" {
		t.Errorf("Clipboard.Get = %q, %v", got, err)
	}
}

func TestBundleSmoke(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	pf := pftest.New(sc, inp, mgr, nil)
	ctx := context.Background()

	t.Run("Input", func(t *testing.T) {
		if err := pf.Input.KeyDown(ctx, "ctrl"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.KeyUp(ctx, "ctrl"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.Type(ctx, "hello"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.MouseClick(ctx, 10, 20, 1); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DoubleClick(ctx, 10, 20); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DragAndDrop(ctx, 0, 0, 100, 100); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Screen", func(t *testing.T) {
		if _, err := pf.Screen.GetPixel(ctx, 5, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := pf.Screen.GetMultiplePixels(ctx, []image.Point{{1, 1}}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Window", func(t *testing.T) {
		mgr.Lists = [][]window.Info{{{
			ID:       1,
			NativeID: "1",
			Title:    "Firefox",
		}}}
		browserWindow, err := pf.Windows.Find(
			ctx,
			perfuncted.WindowMatch{TitleContains: "Firefox"},
		)
		if err != nil {
			t.Fatal(err)
		}
		_ = browserWindow.Resize(ctx, 800, 600)
		_ = browserWindow.Minimize(ctx)
		_ = browserWindow.Maximize(ctx)
		_ = browserWindow.Restore(ctx)
	})
}

type tapErrInputter struct {
	pftest.Inputter
}

type closeErrScreen struct {
	pftest.Screenshotter
	err error
}

func (s *closeErrScreen) Close() error { return s.err }

type closeErrInput struct {
	pftest.Inputter
	err error
}

func (i *closeErrInput) Close() error { return i.err }

type closeErrWindow struct {
	pftest.Manager
	err error
}

func (w *closeErrWindow) Close() error { return w.err }

type closeErrClipboard struct {
	pftest.Clipboard
	err error
}

func (c *closeErrClipboard) Close() error { return c.err }

type contextKey struct{}

type contextSpyInputter struct {
	pftest.Inputter
	ctxValue any
}

func (s *contextSpyInputter) Type(ctx context.Context, text string) error {
	s.ctxValue = ctx.Value(contextKey{})
	return nil
}

type contextSpyManager struct {
	pftest.Manager
	ctxValue any
}

func (m *contextSpyManager) List(ctx context.Context) ([]window.Info, error) {
	m.ctxValue = ctx.Value(contextKey{})
	return []window.Info{{Title: "Firefox"}}, nil
}

type contextSpyClipboard struct {
	pftest.Clipboard
	setValue any
	getValue any
}

func (c *contextSpyClipboard) Get(ctx context.Context) (string, error) {
	c.getValue = ctx.Value(contextKey{})
	return c.Text, nil
}

func (c *contextSpyClipboard) Set(ctx context.Context, text string) error {
	c.setValue = ctx.Value(contextKey{})
	c.Text = text
	return nil
}

type contextSpyScreenshotter struct {
	pftest.Screenshotter
	ctxValue any
}

func (s *contextSpyScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.ctxValue = ctx.Value(contextKey{})
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (m *tapErrInputter) Type(ctx context.Context, s string) error {
	return errors.New("type error")
}

func TestInputBundleErrors(t *testing.T) {
	t.Parallel()
	inp := &tapErrInputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	err := pf.Input.Type(ctx, "a")
	if err == nil || !errors.Is(err, perfuncted.ErrOperationFailed) ||
		!strings.Contains(err.Error(), "type error") {
		t.Errorf("expected an operation failure containing 'type error', got %v", err)
	}
}

func TestOperationErrorPreservesStableErrorCategories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cause      error
		wantFailed bool
		wantCause  error
	}{
		{
			name:       "backend failure",
			cause:      errors.New("backend failed"),
			wantFailed: true,
			wantCause:  nil,
		},
		{
			name:       "unsupported",
			cause:      errors.Join(perfuncted.ErrUnsupported, errors.New("not implemented")),
			wantFailed: false,
			wantCause:  perfuncted.ErrUnsupported,
		},
		{
			name:       "unavailable",
			cause:      perfuncted.ErrUnavailable,
			wantFailed: false,
			wantCause:  perfuncted.ErrUnavailable,
		},
		{
			name:       "invalid argument",
			cause:      perfuncted.ErrInvalidArgument,
			wantFailed: false,
			wantCause:  perfuncted.ErrInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := &perfuncted.OperationError{
				Capability: perfuncted.CapabilityInput,
				Operation:  "test",
				Err:        test.cause,
			}
			if got := errors.Is(err, perfuncted.ErrOperationFailed); got != test.wantFailed {
				t.Fatalf("errors.Is(ErrOperationFailed) = %v, want %v; err = %v", got, test.wantFailed, err)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("errors.Is(%v) = false; err = %v", test.wantCause, err)
			}
		})
	}
}

func TestBundleMethodsPropagateContext(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), contextKey{}, "token")
	inp := &contextSpyInputter{}
	mgr := &contextSpyManager{}
	cb := &contextSpyClipboard{}
	sc := &contextSpyScreenshotter{}
	pf := pftest.New(sc, inp, mgr, cb)

	if err := pf.Input.Type(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.Windows.List(ctx, perfuncted.WindowMatch{}); err != nil {
		t.Fatal(err)
	}
	if err := pf.Clipboard.Set(ctx, "clipboard"); err != nil {
		t.Fatal(err)
	}
	if got, err := pf.Clipboard.Get(ctx); err != nil || got != "clipboard" {
		t.Fatalf("Clipboard.Get = %q, %v", got, err)
	}
	if _, err := pf.Screen.GetPixel(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}

	if inp.ctxValue != "token" {
		t.Fatalf("input ctx = %v, want token", inp.ctxValue)
	}
	if mgr.ctxValue != "token" {
		t.Fatalf("window ctx = %v, want token", mgr.ctxValue)
	}
	if cb.setValue != "token" || cb.getValue != "token" {
		t.Fatalf("clipboard ctx = set:%v get:%v, want token", cb.setValue, cb.getValue)
	}
	if sc.ctxValue != "token" {
		t.Fatalf("screen ctx = %v, want token", sc.ctxValue)
	}
}

func TestCloseJoinsErrors(t *testing.T) {
	t.Parallel()
	screenErr := errors.New("screen close failed")
	inputErr := errors.New("input close failed")
	windowErr := errors.New("window close failed")
	clipboardErr := errors.New("clipboard close failed")

	pf := pftest.New(
		&closeErrScreen{err: screenErr},
		&closeErrInput{err: inputErr},
		&closeErrWindow{err: windowErr},
		&closeErrClipboard{err: clipboardErr},
	)

	err := pf.Close()
	if !errors.Is(err, screenErr) || !errors.Is(err, inputErr) ||
		!errors.Is(err, windowErr) || !errors.Is(err, clipboardErr) {
		t.Fatalf("close error %v does not include all component errors", err)
	}
}

func TestWindowBundle(t *testing.T) {
	mgr := &pftest.Manager{
		Lists: [][]window.Info{
			{{ID: 1, Title: "Firefox"}},
		},
	}
	pf := pftest.New(nil, nil, mgr, nil)
	ctx := context.Background()

	wins, err := pf.Windows.List(ctx, perfuncted.WindowMatch{})
	if err != nil {
		t.Fatal(err)
	}
	if len(wins) != 1 || wins[0].Title() != "Firefox" {
		t.Errorf("unexpected list: %v", wins)
	}

	if err := wins[0].Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Activated) != 1 || mgr.Activated[0] != "1" {
		t.Errorf("unexpected activated: %v", mgr.Activated)
	}
}

func TestScreenBundleHashing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	sc := &legacyHashScreenshotter{
		Screenshotter: &pftest.Screenshotter{Frames: []image.Image{img}},
	}
	pf := pftest.New(sc, nil, nil, nil)
	defer pf.Close()
	ctx := context.Background()
	want := find.PixelHash(img, nil)

	h1, err := pf.Screen.GrabRegionHash(ctx, image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatal(err)
	}
	if h1 != want {
		t.Errorf("GrabRegionHash = %08x, want canonical image hash %08x", h1, want)
	}

	h2, err := pf.Screen.GrabFullHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h1 {
		t.Errorf("expected same hash for full screen, got %08x vs %08x", h1, h2)
	}
	if sc.fullHashCalls != 0 || sc.regionHashCalls != 0 {
		t.Fatalf("legacy fast hash calls = full:%d region:%d, want 0",
			sc.fullHashCalls,
			sc.regionHashCalls,
		)
	}
}

type legacyHashScreenshotter struct {
	*pftest.Screenshotter
	fullHashCalls   int
	regionHashCalls int
}

func (s *legacyHashScreenshotter) CanonicalHashing() bool { return false }

func (s *legacyHashScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	s.fullHashCalls++
	return 0xdeadbeef, nil
}

func (s *legacyHashScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.regionHashCalls++
	return 0xbeefdead, nil
}

func TestWaitForVisibleChange(t *testing.T) {
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2.Set(5, 5, color.RGBA{R: 255, A: 255})

	sc := &pftest.Screenshotter{
		Frames: []image.Image{img1, img1, img2, img2, img2},
	}
	pf := pftest.New(sc, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForSettle(ctx, image.Rect(0, 0, 10, 10), func() error { return nil }, 2, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}

func TestWaitForSettle(t *testing.T) {
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2.Set(5, 5, color.RGBA{R: 255, A: 255})

	sc := &pftest.Screenshotter{
		Frames: []image.Image{img1, img1, img2, img2, img2, img2},
	}
	pf := pftest.New(sc, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForSettle(ctx, image.Rect(0, 0, 10, 10), func() error {
		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}
