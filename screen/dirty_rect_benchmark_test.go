package screen_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/screen"
)

// Separate full-HD buffers model two captures, not aliasing the same frame.
func BenchmarkDirtyRectsFullHD(b *testing.B) {
	for _, sparse := range []bool{false, true} {
		name := "NoChange"
		if sparse {
			name = "Sparse"
		}
		b.Run(name, func(b *testing.B) {
			bounds := image.Rect(0, 0, 1920, 1080)
			before, after := image.NewRGBA(bounds), image.NewRGBA(bounds)
			for i := 0; i < len(before.Pix); i += 4 {
				before.Pix[i], before.Pix[i+1], before.Pix[i+2], before.Pix[i+3] = byte(i/4), byte(i/7680), 64, 255
			}
			copy(after.Pix, before.Pix)
			// Cursor-sized old/new locations and a small status indicator.
			crops := []image.Rectangle{image.Rect(100, 100, 132, 132), image.Rect(160, 140, 192, 172), image.Rect(1800, 20, 1864, 36)}
			if sparse {
				for _, r := range crops {
					for y := r.Min.Y; y < r.Max.Y; y++ {
						for x := r.Min.X; x < r.Max.X; x++ {
							after.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
						}
					}
				}
			}
			want := 0
			if sparse {
				want = len(crops)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, resync, err := screen.DirtyRects(before, after, 64)
				if err != nil || resync || len(got) != want {
					b.Fatalf("rectangles=%v resync=%v err=%v", got, resync, err)
				}
			}
		})
	}
}
