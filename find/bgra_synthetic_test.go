package find

import (
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// Synthetic BGRA frames deliberately include padding, arbitrary source
// alpha, and partial regions to keep the packed-to-canonical hash contract.
func syntheticBGRAFrame(width, height, padding int, seed int64) ([]byte, *image.RGBA, int) {
	stride := 4*width + padding
	data := make([]byte, height*stride)
	rng := rand.New(rand.NewSource(seed))
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := y*stride + x*4
			r, g, b := byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256))
			data[offset] = b
			data[offset+1] = g
			data[offset+2] = r
			data[offset+3] = byte(rng.Intn(256)) // compositor alpha is irrelevant
			rgba.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return data, rgba, stride
}

func TestSyntheticBGRAEquivalence(t *testing.T) {
	for _, tc := range []struct{ width, height, pad int }{
		{1, 1, 0}, {3, 9, 7}, {32, 24, 8}, {127, 11, 3}, {512, 64, 16},
	} {
		data, reference, stride := syntheticBGRAFrame(tc.width, tc.height, tc.pad, int64(tc.width*99+tc.height))
		regions := []image.Rectangle{
			image.Rectangle{}, reference.Bounds(),
			image.Rect(0, 0, 1, 1),
			image.Rect(tc.width/3, tc.height/3, tc.width, tc.height),
		}
		for _, region := range regions {
			got, err := PixelHashBGRA(data, tc.width, tc.height, stride, region)
			if err != nil {
				t.Fatalf("size=%dx%d region=%v: %v", tc.width, tc.height, region, err)
			}
			refRegion := region
			if region.Empty() {
				refRegion = reference.Bounds()
			}
			want := PixelHash(reference.SubImage(refRegion), nil)
			if got != want {
				t.Fatalf("size=%dx%d region=%v: hash %08x, want %08x", tc.width, tc.height, region, got, want)
			}
		}
	}
}

func BenchmarkSyntheticBGRA512(b *testing.B) {
	const width, height, padding = 512, 512, 16
	data, reference, stride := syntheticBGRAFrame(width, height, padding, 74)
	want := PixelHash(reference, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := PixelHashBGRA(data, width, height, stride, image.Rectangle{})
		if err != nil || got != want {
			b.Fatalf("hash=%x expected=%x err=%v", got, want, err)
		}
	}
}
