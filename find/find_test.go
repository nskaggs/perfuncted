package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

// ── PixelHash ─────────────────────────────────────────────────────────────────

func TestPixelHashDeterministic(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 50), G: uint8(y * 50), B: 100, A: 255})
		}
	}
	h1 := PixelHash(img, nil)
	h2 := PixelHash(img, nil)
	if h1 != h2 {
		t.Fatalf("same image gave different hashes: %08x vs %08x", h1, h2)
	}
}

func TestPixelHashDiffersForDifferentImages(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})

	ha := PixelHash(a, nil)
	hb := PixelHash(b, nil)
	if ha == hb {
		t.Fatal("different images should (almost certainly) have different hashes")
	}
}

func TestPixelHashSubImage(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			full.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	sub := full.SubImage(image.Rect(2, 2, 5, 5)).(*image.RGBA)

	// Build an equivalent standalone image.
	equiv := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			equiv.SetRGBA(x, y, full.RGBAAt(x+2, y+2))
		}
	}

	hSub := PixelHash(sub, nil)
	hEquiv := PixelHash(equiv, nil)
	if hSub != hEquiv {
		t.Fatalf("subimage hash %08x != equivalent %08x", hSub, hEquiv)
	}
}

// ── FirstPixel / LastPixel ────────────────────────────────────────────────────

type fakeScreen struct {
	img *image.RGBA
}

func (f *fakeScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return f.img.SubImage(rect), nil
}

func (f *fakeScreen) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(f.img, nil), nil
}

func (f *fakeScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := f.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

func TestFirstPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.SetRGBA(3, 3, color.RGBA{R: 42, G: 84, B: 126, A: 255})
	sc := &fakeScreen{img: img}

	c, err := FirstPixel(context.Background(), sc, image.Rect(3, 3, 6, 6))
	if err != nil {
		t.Fatal(err)
	}
	if c.R != 42 || c.G != 84 || c.B != 126 {
		t.Fatalf("expected (42,84,126) got (%d,%d,%d)", c.R, c.G, c.B)
	}
}

func TestLastPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.SetRGBA(5, 5, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	sc := &fakeScreen{img: img}

	c, err := LastPixel(context.Background(), sc, image.Rect(3, 3, 6, 6))
	if err != nil {
		t.Fatal(err)
	}
	if c.R != 10 || c.G != 20 || c.B != 30 {
		t.Fatalf("expected (10,20,30) got (%d,%d,%d)", c.R, c.G, c.B)
	}
}

// ── matchAt ───────────────────────────────────────────────────────────────────

func TestMatchAt(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))

	// Fill a 2x2 patch in src at (3,3).
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			c := color.RGBA{R: uint8(x*100 + y*50), G: 0, B: 0, A: 255}
			src.SetRGBA(3+x, 3+y, c)
			ref.SetRGBA(x, y, c)
		}
	}

	if !matchAt(src, ref, 3, 3) {
		t.Fatal("should match at (3,3)")
	}
	if matchAt(src, ref, 0, 0) {
		t.Fatal("should not match at (0,0)")
	}
}

// ── LocateExact ───────────────────────────────────────────────────────────────

func TestLocateExact(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))

	// Place a recognizable pattern at (5,7).
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			c := color.RGBA{R: 200, G: uint8(x * 80), B: uint8(y * 80), A: 255}
			img.SetRGBA(5+x, 7+y, c)
			needle.SetRGBA(x, y, c)
		}
	}

	sc := &fakeScreen{img: img}
	found, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 20, 20), needle)
	if err != nil {
		t.Fatal(err)
	}
	if found.Min.X != 5 || found.Min.Y != 7 {
		t.Fatalf("expected (5,7), got (%d,%d)", found.Min.X, found.Min.Y)
	}
	if found.Dx() != 3 || found.Dy() != 3 {
		t.Fatalf("expected 3x3, got %dx%d", found.Dx(), found.Dy())
	}
}

// ── uniqueRunes (tested via xkb helper in input package, sanity check here) ──

func TestAnchorRect(t *testing.T) {
	a := Anchor{X: 100, Y: 200}
	r := a.Rect(10, 20, 50, 30)
	if r.Min.X != 110 || r.Min.Y != 220 || r.Dx() != 50 || r.Dy() != 30 {
		t.Fatalf("unexpected rect: %v", r)
	}
}
