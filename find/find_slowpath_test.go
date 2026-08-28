package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

// nrgbaScreen returns *image.NRGBA images, exercising all non-RGBA slow paths.
type nrgbaScreen struct {
	img *image.NRGBA
}

func (s *nrgbaScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return s.img, nil
}

func (s *nrgbaScreen) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(s.img, nil), nil
}

func (s *nrgbaScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return PixelHash(s.img, nil), nil
}

// solidNRGBA returns a w×h *image.NRGBA filled with c.
func solidNRGBA(w, h int, c color.RGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	nc := color.NRGBA(c)
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, nc)
		}
	}
	return img
}

// ── colorClose (via PixelFound slow path) ────────────────────────────────────

// TestColorClose exercises the colorClose helper through the PixelFound slow
// path, which is only reached when img is not *image.RGBA.
func TestColorClose(t *testing.T) {
	img := solidNRGBA(4, 4, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	rect := img.Bounds()

	// Within tolerance: each channel within tol=10.
	target := color.RGBA{R: 205, G: 95, B: 55, A: 255}
	if _, ok := PixelFound(img, rect, target, 10); !ok {
		t.Error("expected PixelFound (slow path) to find pixel within tolerance=10")
	}

	// Exactly at tolerance boundary.
	target2 := color.RGBA{R: 210, G: 90, B: 40, A: 255}
	if _, ok := PixelFound(img, rect, target2, 10); !ok {
		t.Error("expected PixelFound to find pixel at exact tolerance boundary")
	}

	// Outside tolerance: R channel exceeds tol.
	target3 := color.RGBA{R: 215, G: 100, B: 50, A: 255}
	if _, ok := PixelFound(img, rect, target3, 10); ok {
		t.Error("expected PixelFound to NOT find pixel outside tolerance")
	}
}

// ── PixelFound slow path ──────────────────────────────────────────────────────

// TestPixelFound_SlowPath verifies PixelFound with a non-RGBA source image.
func TestPixelFound_SlowPath(t *testing.T) {
	img := solidNRGBA(8, 8, color.RGBA{R: 50, G: 50, B: 50, A: 255})
	// Place a distinctive red pixel at (3, 5).
	img.SetNRGBA(3, 5, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	rect := img.Bounds()

	pt, ok := PixelFound(img, rect, color.RGBA{R: 255, G: 0, B: 0, A: 255}, 0)
	if !ok {
		t.Fatal("PixelFound slow path: expected to find red pixel")
	}
	if pt.X != 3 || pt.Y != 5 {
		t.Fatalf("PixelFound slow path: got (%d,%d), want (3,5)", pt.X, pt.Y)
	}

	// Not found case.
	if _, ok := PixelFound(img, rect, color.RGBA{R: 0, G: 255, B: 0, A: 255}, 0); ok {
		t.Fatal("PixelFound slow path: expected no match for green in a non-green image")
	}
}

// TestPixelFound_SlowPath_AbsoluteCoord verifies that the returned point is in
// absolute screen coordinates when the image rect is offset.
func TestPixelFound_SlowPath_AbsoluteCoord(t *testing.T) {
	// Allocate image at offset (10, 20) to simulate absolute-bounds images.
	img := image.NewNRGBA(image.Rect(10, 20, 14, 24))
	img.SetNRGBA(12, 22, color.NRGBA{R: 0, G: 0, B: 200, A: 255})

	// rect argument tells PixelFound the absolute capture bounds.
	captureRect := image.Rect(10, 20, 14, 24)
	pt, ok := PixelFound(img, captureRect, color.RGBA{R: 0, G: 0, B: 200, A: 255}, 0)
	if !ok {
		t.Fatal("PixelFound slow path (offset): expected to find blue pixel")
	}
	// PixelFound maps (x=12, y=22) → abs = rect.Min + (x-b.Min.X, y-b.Min.Y)
	//   = (10,20) + (12-10, 22-20) = (12, 22).
	if pt.X != 12 || pt.Y != 22 {
		t.Fatalf("PixelFound slow path (offset): got (%d,%d), want (12,22)", pt.X, pt.Y)
	}
}

// ── matchAt slow path ─────────────────────────────────────────────────────────

// TestMatchAt_SlowPath verifies matchAt with non-RGBA (NRGBA) images.
func TestMatchAt_SlowPath(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	ref := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	// Fill a 2×2 patch in src at (4, 6).
	for y := range 2 {
		for x := range 2 {
			c := color.NRGBA{R: uint8(x*100 + 50), G: uint8(y*100 + 50), B: 0, A: 255}
			src.SetNRGBA(4+x, 6+y, c)
			ref.SetNRGBA(x, y, c)
		}
	}

	if !matchAt(src, ref, 4, 6) {
		t.Fatal("matchAt slow path: should match at (4,6)")
	}
	if matchAt(src, ref, 0, 0) {
		t.Fatal("matchAt slow path: should not match at (0,0)")
	}
}

// ── LocateExact slow path ──────────────────────────────────────────────────────

// TestLocateExact_SlowPath verifies LocateExact when both src and ref are non-RGBA.
func TestLocateExact_SlowPath(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	ref := image.NewNRGBA(image.Rect(0, 0, 3, 3))

	// Fill a 3×3 pattern in src at (7, 9) and the reference.
	for y := range 3 {
		for x := range 3 {
			c := color.NRGBA{R: 180, G: uint8(x * 70), B: uint8(y * 70), A: 255}
			src.SetNRGBA(7+x, 9+y, c)
			ref.SetNRGBA(x, y, c)
		}
	}

	sc := &nrgbaScreen{img: src}
	found, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 20, 20), ref)
	if err != nil {
		t.Fatalf("LocateExact slow path: unexpected error: %v", err)
	}
	if found.Min.X != 7 || found.Min.Y != 9 {
		t.Fatalf("LocateExact slow path: got min (%d,%d), want (7,9)", found.Min.X, found.Min.Y)
	}
	if found.Dx() != 3 || found.Dy() != 3 {
		t.Fatalf("LocateExact slow path: got size (%d,%d), want (3,3)", found.Dx(), found.Dy())
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkPixelFound_FastPath benchmarks PixelFound with *image.RGBA (fast path).
func BenchmarkPixelFound_FastPath(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := range 256 {
			img.SetRGBA(x, y, color.RGBA{R: 80, G: 80, B: 80, A: 255})
		}
	}
	img.SetRGBA(200, 200, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	rect := img.Bounds()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = PixelFound(img, rect, target, 0)
	}
}

// BenchmarkPixelFound_NRGBA benchmarks the native screenshot image layout.
func BenchmarkPixelFound_NRGBA(b *testing.B) {
	img := solidNRGBA(256, 256, color.RGBA{R: 80, G: 80, B: 80, A: 255})
	img.SetNRGBA(200, 200, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	rect := img.Bounds()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = PixelFound(img, rect, target, 0)
	}
}

func TestPixelFoundNRGBAAllocations(t *testing.T) {
	img := solidNRGBA(256, 256, color.RGBA{R: 80, G: 80, B: 80, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = PixelFound(img, img.Bounds(), target, 0)
	})
	if allocs != 0 {
		t.Fatalf("PixelFound NRGBA allocations = %v, want 0", allocs)
	}
}
