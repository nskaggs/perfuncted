package find

import (
	"context"
	"hash/crc32"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// syntheticImage deliberately exposes only image.Image, not SubImage. Real
// screenshot wrappers and remote capture providers need not implement SubImage.
type syntheticImage struct{ img image.Image }

func (s syntheticImage) Bounds() image.Rectangle { return s.img.Bounds() }
func (s syntheticImage) ColorModel() color.Model { return s.img.ColorModel() }
func (s syntheticImage) At(x, y int) color.Color { return s.img.At(x, y) }

type syntheticScreen struct {
	source     image.Image
	zeroOrigin bool
	grabs      []image.Rectangle
}

func (s *syntheticScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.grabs = append(s.grabs, rect)
	bounds := rect
	if s.zeroOrigin {
		bounds = image.Rect(0, 0, rect.Dx(), rect.Dy())
	}
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, s.source, rect.Min, draw.Src)
	return syntheticImage{dst}, nil
}

// independentRegionHash converts precisely one requested region using
// draw.Draw and standard-library CRC32 instead of the optimized scanner.
func independentRegionHash(img image.Image, rect image.Rectangle) uint32 {
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	return crc32.ChecksumIEEE(dst.Pix)
}

func syntheticScanFixture() (image.Image, []image.Rectangle, []uint32) {
	src := image.NewNRGBA(image.Rect(50, 70, 86, 82))
	rects := []image.Rectangle{
		image.Rect(50, 70, 62, 82),
		image.Rect(62, 70, 74, 82),
		image.Rect(74, 70, 86, 82),
	}
	for y := 70; y < 82; y++ {
		for x := 50; x < 86; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(20 + x*3), G: uint8(11 + y*2), B: uint8(x ^ y), A: uint8(150 + (x+y)%106)})
		}
	}
	wants := []uint32{0, 0, independentRegionHash(src, rects[2])}
	return src, rects, wants
}

func TestSyntheticScanCompactUsesOneCaptureWithoutSubImage(t *testing.T) {
	src, rects, wants := syntheticScanFixture()
	for _, zeroOrigin := range []bool{true, false} {
		screen := &syntheticScreen{source: src, zeroOrigin: zeroOrigin}
		result, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
		if err != nil {
			t.Fatalf("origin=%v: %v", zeroOrigin, err)
		}
		if result.Rect != rects[2] || result.Hash != wants[2] {
			t.Fatalf("origin=%v: got %+v, want region %v hash %08x", zeroOrigin, result, rects[2], wants[2])
		}
		if len(screen.grabs) != 1 || screen.grabs[0] != image.Rect(50, 70, 86, 82) {
			t.Fatalf("origin=%v: captures=%v, want one compact capture", zeroOrigin, screen.grabs)
		}
	}
}

func BenchmarkSyntheticScanCompactGeneric(b *testing.B) {
	src, rects, wants := syntheticScanFixture()
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		screen.grabs = screen.grabs[:0]
		got, err := ScanFor(ctx, screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[2] {
			b.Fatalf("result=%+v err=%v", got, err)
		}
		if len(screen.grabs) != 1 {
			b.Fatalf("performed %d backend captures instead of 1", len(screen.grabs))
		}
	}
}

// Sparse regions must not trigger a potentially huge union screenshot.
func TestSyntheticScanSparseAvoidsLargeCapture(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	rects := []image.Rectangle{image.Rect(1, 1, 13, 13), image.Rect(240, 240, 252, 252)}
	src.SetNRGBA(241, 241, color.NRGBA{R: 246, A: 255})
	wants := []uint32{0, independentRegionHash(src, rects[1])}
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	result, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
	if err != nil || result.Rect != rects[1] {
		t.Fatalf("scan: %+v, %v", result, err)
	}
	if len(screen.grabs) != 2 || screen.grabs[0] != rects[0] || screen.grabs[1] != rects[1] {
		t.Fatalf("sparse scans grabbed %v instead of only the two requested regions", screen.grabs)
	}
}

func TestSyntheticScanCompactCustomHasher(t *testing.T) {
	src, rects, _ := syntheticScanFixture()
	reference := image.NewRGBA(image.Rect(0, 0, rects[2].Dx(), rects[2].Dy()))
	draw.Draw(reference, reference.Bounds(), src, rects[2].Min, draw.Src)
	h := fnv.New32a()
	_, _ = h.Write(reference.Pix)
	wants := []uint32{0, 0, h.Sum32()}
	screen := &syntheticScreen{source: src, zeroOrigin: false}
	result, err := ScanFor(context.Background(), screen, rects, wants, 0, fnv.New32a)
	if err != nil || result.Rect != rects[2] || result.Hash != wants[2] {
		t.Fatalf("custom hash result=%+v, err=%v", result, err)
	}
	if len(screen.grabs) != 1 {
		t.Fatalf("custom hash took %d captures, want 1", len(screen.grabs))
	}
}

// A dense cluster of overlapping UI watch rectangles is captured once, but
// each requested rectangle must still be checked in caller order and hashed
// against the same canonical premultiplied RGBA representation as Grab.
func syntheticOverlappingFixture() (image.Image, []image.Rectangle, []uint32) {
	source := image.NewNRGBA(image.Rect(50, 70, 94, 114))
	for y := 70; y < 114; y++ {
		for x := 50; x < 94; x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 7), G: uint8(y * 3), B: uint8(x ^ y),
				A: uint8(80 + (x+y)%176),
			})
		}
	}
	rects := make([]image.Rectangle, 0, 16)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			start := image.Pt(50+4*x, 70+4*y)
			rects = append(rects, image.Rectangle{Min: start, Max: start.Add(image.Pt(32, 32))})
		}
	}
	wants := make([]uint32, len(rects))
	wants[len(wants)-1] = independentRegionHash(source, rects[len(rects)-1])
	return source, rects, wants
}

func TestSyntheticScanOverlappingCanonicalAndCaptureCount(t *testing.T) {
	src, rects, wants := syntheticOverlappingFixture()
	for _, zeroOrigin := range []bool{false, true} {
		screen := &syntheticScreen{source: src, zeroOrigin: zeroOrigin}
		got, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[len(rects)-1] || got.Hash != wants[len(wants)-1] {
			t.Fatalf("zeroOrigin=%v: got=%+v err=%v", zeroOrigin, got, err)
		}
		if len(screen.grabs) != 1 || screen.grabs[0] != src.Bounds() {
			t.Fatalf("zeroOrigin=%v: grabs=%v", zeroOrigin, screen.grabs)
		}
	}
}

func BenchmarkSyntheticScanOverlappingGeneric(b *testing.B) {
	src, rects, wants := syntheticOverlappingFixture()
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		screen.grabs = screen.grabs[:0]
		got, err := ScanFor(ctx, screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[len(rects)-1] || len(screen.grabs) != 1 {
			b.Fatalf("scan got=%+v err=%v captures=%d", got, err, len(screen.grabs))
		}
	}
}
