package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

// BenchmarkMatchTemplatesInImage measures the full-HD negative case and
// bounded dense all-matches retrieval without desktop capture overhead.
func BenchmarkMatchTemplatesInImage(b *testing.B) {
	for _, tc := range []struct {
		name string
		size image.Point
		fill bool
	}{
		{"fullHD-no-match", image.Pt(1920, 1080), false},
		{"dense-64-matches", image.Pt(128, 64), true},
		{"fullHD-dense-64", image.Pt(1920, 1080), true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			src := image.NewNRGBA(image.Rect(0, 0, tc.size.X, tc.size.Y))
			ref := image.NewNRGBA(image.Rect(0, 0, 4, 4))
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					ref.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
				}
			}
			if tc.fill {
				for y := 0; y < tc.size.Y; y++ {
					for x := 0; x < tc.size.X; x++ {
						src.SetNRGBA(x, y, color.NRGBA{R: 254, A: 255})
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matches, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 0.99, 64)
				if err != nil {
					b.Fatal(err)
				}
				if tc.fill && len(matches) != 64 {
					b.Fatalf("dense count=%d", len(matches))
				}
				if !tc.fill && len(matches) != 0 {
					b.Fatalf("unexpected negative matches=%d", len(matches))
				}
			}
		})
	}
}
