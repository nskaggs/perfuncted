package main

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Measures the real PNG/base64/JSON encoder for sparse crops of a synthetic
// full-HD capture. Unchanged frames are suppressed by the watch path.
func BenchmarkWatchPixelFrameFullHD(b *testing.B) {
	b.Run("Sparse", func(b *testing.B) {
		bounds := image.Rect(0, 0, 1920, 1080)
		img := image.NewRGBA(bounds)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(i/4), byte(i/7680), 64, 255
		}
		capture := perfuncted.Capture{Image: img, ScreenRect: bounds}
		crops := []image.Rectangle{image.Rect(100, 100, 132, 132), image.Rect(160, 140, 192, 172), image.Rect(1800, 20, 1864, 36)}
		frame := watchChangeFrame{GeometryVersion: 1}
		ctx := context.Background()
		var wire benchmarkWireCounter
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := writeWatchPixelFrame(ctx, &wire, frame, capture, crops, watchPixelFrameLimit); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(wire)/float64(b.N), "wire-B/frame")
	})
}

type benchmarkWireCounter int64

func (c *benchmarkWireCounter) Write(p []byte) (int, error) {
	*c += benchmarkWireCounter(len(p))
	return len(p), nil
}
