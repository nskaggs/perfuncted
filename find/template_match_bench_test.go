package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

func BenchmarkMatchTemplateFullHD(b *testing.B) {
	const (
		width          = 1920
		height         = 1080
		templateWidth  = 16
		templateHeight = 16
	)
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint32(x*73856093 ^ y*19349663)
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255})
		}
	}
	reference := image.NewNRGBA(image.Rect(0, 0, templateWidth, templateHeight))
	for y := 0; y < templateHeight; y++ {
		for x := 0; x < templateWidth; x++ {
			value := uint32((x+17)*83492791 ^ (y+11)*2971215073)
			reference.SetNRGBA(x, y, color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255})
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(width * height * 4))
	b.ResetTimer()
	for b.Loop() {
		_, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, 0.95)
		if !errors.Is(err, ErrNotFound) {
			b.Fatalf("MatchTemplateInImage error = %v, want ErrNotFound", err)
		}
	}
}
