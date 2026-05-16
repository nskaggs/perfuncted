package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

func BenchmarkPixelHashRGBA(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PixelHash(img, nil)
	}
}

func BenchmarkLocateExactRGBA(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 256, 256))
	ref := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 50, G: 60, B: 70, A: 255})
		}
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			c := color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: 99, A: 255}
			src.SetRGBA(180+x, 160+y, c)
			ref.SetRGBA(x, y, c)
		}
	}
	sc := &fakeScreen{img: src}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 256, 256), ref); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScanForCompact(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 25, G: 25, B: 25, A: 255})
		}
	}
	img.SetRGBA(16, 16, color.RGBA{R: 200, G: 0, B: 0, A: 255})
	img.SetRGBA(80, 80, color.RGBA{R: 0, G: 200, B: 0, A: 255})

	sc := &fakeScreen{img: img}
	rects := []image.Rectangle{image.Rect(0, 0, 32, 32), image.Rect(64, 64, 96, 96)}
	wants := make([]uint32, len(rects))
	for i, r := range rects {
		wants[i] = PixelHash(img.SubImage(r), nil)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ScanFor(context.Background(), sc, rects, wants, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}
