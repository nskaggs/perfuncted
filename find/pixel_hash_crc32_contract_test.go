package find

import (
	"hash/adler32"
	"hash/crc32"
	"image"
	"image/color"
	"testing"
)

var pixelHashContractSink uint32

func TestPixelHashDirectCRC32MatchesExplicitHasher(t *testing.T) {
	full := image.NewRGBA(image.Rect(11, 19, 139, 83))
	for y := full.Rect.Min.Y; y < full.Rect.Max.Y; y++ {
		for x := full.Rect.Min.X; x < full.Rect.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 7), B: uint8(x ^ y), A: 255})
		}
	}
	nrgba := image.NewNRGBA(full.Bounds())
	for y := nrgba.Rect.Min.Y; y < nrgba.Rect.Max.Y; y++ {
		for x := nrgba.Rect.Min.X; x < nrgba.Rect.Max.X; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 21, A: uint8(32 + (x+y)%200)})
		}
	}
	cases := []struct {
		name string
		img  image.Image
	}{
		{"full RGBA", full},
		{"padded RGBA subimage", full.SubImage(image.Rect(25, 30, 72, 68))},
		{"full-width subimage", full.SubImage(image.Rect(11, 36, 139, 57))},
		{"nonopaque NRGBA", nrgba},
		{"subregion NRGBA", nrgba.SubImage(image.Rect(24, 27, 85, 61))},
		{"gray", image.NewGray(image.Rect(-5, -6, 12, 18))},
		{"empty RGBA", image.NewRGBA(image.Rectangle{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PixelHash(tc.img, nil)
			want := PixelHash(tc.img, crc32.NewIEEE)
			if got != want {
				t.Fatalf("canonical hash=%08x explicit CRC32=%08x", got, want)
			}
			custom := PixelHash(tc.img, adler32.New)
			if custom == got && tc.img.Bounds().Dx() > 0 && tc.img.Bounds().Dy() > 0 {
				t.Fatalf("custom Adler32 unexpectedly identical to CRC32: %08x", custom)
			}
		})
	}
}
func TestPixelHashDefaultRGBANoHeapAllocation(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 64, 64))
	subset := full.SubImage(image.Rect(7, 7, 47, 45))
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"full", full}, {"subregion", subset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() { pixelHashContractSink = PixelHash(tc.img, nil) })
			if allocs != 0 {
				t.Fatalf("default RGBA hash allocations = %g, want 0", allocs)
			}
		})
	}
}

// DefaultHasher is an assignable public variable, not a fixed alias. A custom
// default must keep working when callers request a nil hash factory.
func TestPixelHashRespectsDefaultHasherOverride(t *testing.T) {
	original := DefaultHasher
	DefaultHasher = adler32.New
	t.Cleanup(func() { DefaultHasher = original })
	img := image.NewRGBA(image.Rect(0, 0, 19, 13))
	for y := 0; y < 13; y++ {
		for x := 0; x < 19; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 19), B: 57, A: 255})
		}
	}
	got := PixelHash(img, nil)
	want := PixelHash(img, adler32.New)
	if got != want {
		t.Fatalf("modified default hash=%08x, explicit Adler32=%08x", got, want)
	}
	if got == PixelHash(img, crc32.NewIEEE) {
		t.Fatal("mutable default override was ignored")
	}
}
