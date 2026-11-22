package screen

import (
	"image"
	"image/png"
	"io"
	"testing"
)

// BenchmarkCapturePNGEncode measures optional caller work on an in-memory image
// fixture. It is not a screenshot latency benchmark.
func BenchmarkCapturePNGEncode(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := png.Encode(io.Discard, img); err != nil {
			b.Fatal(err)
		}
	}
}
