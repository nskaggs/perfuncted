package screen

import (
	"image"
	"image/color"
	"testing"
)

// ── Edge-case coverage ────────────────────────────────────────────────────────

// TestDecodeBGRARectEmptyRect verifies the early-return branch when the
// requested rectangle does not intersect the image bounds.
func TestDecodeBGRARectEmptyRect(t *testing.T) {
	data := make([]byte, 4*4*4) // 4×4 image, stride=16
	// Request a rect entirely outside the 4×4 frame.
	out := decodeBGRARect(data, 4, 4, 16, image.Rect(10, 10, 20, 20))
	if !out.Bounds().Empty() {
		t.Fatalf("expected empty output for out-of-bounds rect, got %v", out.Bounds())
	}
}

// TestCropRGBA_IdentityReturn verifies the early-return branch where the crop
// rectangle exactly matches the source image bounds starting at the origin.
func TestCropRGBA_IdentityReturn(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5, 5))
	src.SetRGBA(2, 3, color.RGBA{R: 42, G: 84, B: 126, A: 255})

	result := cropRGBA(src, image.Rect(0, 0, 5, 5))
	if result != src {
		t.Fatal("cropRGBA should return src unchanged when rect equals full bounds at origin")
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkDecodeBGRA(b *testing.B) {
	const w, h = 1920, 1080
	data := make([]byte, w*h*4)
	// Fill with non-trivial pattern.
	for i := range data {
		data[i] = byte(i)
	}

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = decodeBGRA(data, w, h, w*4)
	}
}

func BenchmarkDecodeBGRARect(b *testing.B) {
	const w, h = 1920, 1080
	data := make([]byte, w*h*4)
	for i := range data {
		data[i] = byte(i)
	}
	// Crop a central 400×300 region.
	rect := image.Rect(760, 390, 1160, 690)

	b.SetBytes(int64(rect.Dx() * rect.Dy() * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = decodeBGRARect(data, w, h, w*4, rect)
	}
}

func BenchmarkCropRGBA(b *testing.B) {
	const w, h = 1920, 1080
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = byte(i)
	}
	rect := image.Rect(100, 100, 500, 400)

	b.SetBytes(int64(rect.Dx() * rect.Dy() * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cropRGBA(src, rect)
	}
}
