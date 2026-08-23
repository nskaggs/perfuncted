package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

type divergentHashScreenshotter struct {
	img         image.Image
	regionHash  uint32
	regionCalls int
}

func (s *divergentHashScreenshotter) Grab(_ context.Context, rect image.Rectangle) (image.Image, error) {
	if sub, ok := s.img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(rect), nil
	}
	return s.img, nil
}

func (s *divergentHashScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.regionCalls++
	return s.regionHash, nil
}

func TestGrabHashDefaultMatchesPixelHashOfGrab(t *testing.T) {
	tests := []struct {
		name string
		img  image.Image
	}{
		{name: "RGBA backend", img: testHashImageRGBA()},
		{name: "NRGBA backend", img: testHashImageNRGBA()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &divergentHashScreenshotter{img: tt.img, regionHash: 0xdeadbeef}
			rect := image.Rect(11, 21, 13, 23)

			got, err := GrabHash(context.Background(), sc, rect, nil)
			if err != nil {
				t.Fatalf("GrabHash: %v", err)
			}
			grabbed, err := sc.Grab(context.Background(), rect)
			if err != nil {
				t.Fatalf("Grab: %v", err)
			}
			want := PixelHash(grabbed, nil)
			if got != want {
				t.Fatalf("GrabHash = %08x, want PixelHash(Grab(...)) = %08x", got, want)
			}
			if sc.regionCalls != 0 {
				t.Fatalf("GrabRegionHash calls = %d, want 0", sc.regionCalls)
			}
		})
	}
}

func testHashImageRGBA() image.Image {
	img := image.NewRGBA(image.Rect(10, 20, 14, 24))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	return img
}

func testHashImageNRGBA() image.Image {
	img := image.NewNRGBA(image.Rect(10, 20, 14, 24))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	return img
}
