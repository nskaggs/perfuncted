package perfuncted_test

import (
	"image"
	"image/color"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestModifierDownUp(t *testing.T) {
	m := &pftest.Inputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.ModifierDown("ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := inp.ModifierUp("ctrl"); err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 2 || m.Calls[0] != "down:ctrl" || m.Calls[1] != "up:ctrl" {
		t.Fatalf("unexpected calls: %v", m.Calls)
	}
}

func TestTypeWithDelay(t *testing.T) {
	m := &pftest.Inputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.TypeWithDelay("ab", 1*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := m.Typed(); got != "ab" {
		t.Fatalf("Typed() = %q, want %q", got, "ab")
	}
}

// rawInputter embeds pftest.Inputter and exposes a Raw method for tests.
type rawInputter struct {
	*pftest.Inputter
	rawCalls []int
}

func (r *rawInputter) Raw(sc int) error {
	r.rawCalls = append(r.rawCalls, sc)
	return nil
}

func TestRawScancode(t *testing.T) {
	r := &rawInputter{Inputter: &pftest.Inputter{}}
	inp := perfuncted.InputBundle{Inputter: r}
	if err := inp.Raw(42); err != nil {
		t.Fatal(err)
	}
	if len(r.rawCalls) != 1 || r.rawCalls[0] != 42 {
		t.Fatalf("raw calls = %v, want [42]", r.rawCalls)
	}
}

func TestScreenGetPixelMultipleCapture(t *testing.T) {
	// build a 2x2 image with distinct pixels
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 10, G: 0, B: 0, A: 255})
	img.SetRGBA(1, 0, color.RGBA{R: 0, G: 20, B: 0, A: 255})
	img.SetRGBA(0, 1, color.RGBA{R: 0, G: 0, B: 30, A: 255})
	img.SetRGBA(1, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})

	sc := &pftest.Screenshotter{Frames: []image.Image{img}}
	s := perfuncted.ScreenBundle{Screenshotter: sc}

	c, err := s.GetPixel(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.R != 10 {
		t.Fatalf("got R=%d want 10", c.R)
	}

	pts := []image.Point{{0, 0}, {1, 1}}
	cols, err := s.GetMultiplePixels(pts)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 {
		t.Fatalf("len(cols)=%d", len(cols))
	}
	if cols[1].R != 40 || cols[1].G != 50 || cols[1].B != 60 {
		t.Fatalf("unexpected second pixel: %#v", cols[1])
	}

	// CaptureRegion writes a PNG file
	f, err := os.CreateTemp("", "pf-test-*.png")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if err := s.CaptureRegion(image.Rect(0, 0, 2, 2), path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() == 0 {
		t.Fatalf("empty capture file: %s", path)
	}

	// PixelToScreen should return an image
	out, err := s.PixelToScreen(image.Rect(0, 0, 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 2 {
		t.Fatalf("unexpected bounds: %v", out.Bounds())
	}
}

func TestWindowGeometryAndProcess(t *testing.T) {
	mgr := &pftest.Manager{
		Lists: [][]window.Info{{
			{Title: "MyApp", X: 10, Y: 20, W: 30, H: 40, PID: 123},
		}},
	}
	// Note: pftest.Manager uses window.Info in package window; use perfuncted.WindowBundle
	w := perfuncted.WindowBundle{Manager: mgr}

	rect, err := w.GetGeometry("MyApp")
	if err != nil {
		t.Fatal(err)
	}
	if rect != image.Rect(10, 20, 40, 60) {
		t.Fatalf("rect=%v", rect)
	}
	pid, err := w.GetProcess("MyApp")
	if err != nil {
		t.Fatal(err)
	}
	if pid != 123 {
		t.Fatalf("pid=%d", pid)
	}

	// Resize/Minimize/Maximize should not error (pftest.Manager returns nil by default)
	if err := w.Resize("MyApp", 200, 200); err != nil {
		t.Fatal(err)
	}
	if err := w.Minimize("MyApp"); err != nil {
		t.Fatal(err)
	}
	if err := w.Maximize("MyApp"); err != nil {
		t.Fatal(err)
	}

	// Restore should attempt to activate the window; pftest.Manager records activations
	if err := w.Restore("MyApp"); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Activated) == 0 {
		t.Fatalf("expected activation recorded, got none")
	}
}
