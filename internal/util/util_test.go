package util_test

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestWaitForPixelColor(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	black := color.RGBA{A: 255}
	img1 := pftest.SolidImage(2, 2, black)
	img2 := pftest.SolidImage(2, 2, red)
	sc := &pftest.Screenshotter{Frames: []image.Image{img1, img2}}
	ok, err := util.WaitForPixelColor(sc, image.Rect(0, 0, 2, 2), red, 0, 200*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("WaitForPixelColor failed: %v ok=%v", err, ok)
	}
}

func TestWaitForImageExact(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 4, 4))
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// fill ref with distinct values
	ref.SetRGBA(0, 0, color.RGBA{R: 10, A: 255})
	ref.SetRGBA(1, 0, color.RGBA{G: 20, A: 255})
	ref.SetRGBA(0, 1, color.RGBA{B: 30, A: 255})
	ref.SetRGBA(1, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	// place ref at (1,1) inside big
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			big.SetRGBA(1+x, 1+y, ref.RGBAAt(x, y))
		}
	}
	sc := &pftest.Screenshotter{Frames: []image.Image{big}}
	res, err := util.WaitForImage(sc, ref, "exact", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Rect != image.Rect(1, 1, 3, 3) {
		t.Fatalf("unexpected rect: %v", res[0].Rect)
	}
}

func TestImageHashCompare(t *testing.T) {
	r1 := pftest.SolidImage(2, 2, color.RGBA{R: 255, A: 255})
	r2 := pftest.SolidImage(2, 2, color.RGBA{G: 255, A: 255})
	h1 := find.PixelHash(r1, nil)
	h2 := find.PixelHash(r2, nil)
	if !util.ImageHashCompare(h1, h1, 0) {
		t.Error("identical hashes should compare equal at tolerance 0")
	}
	if util.ImageHashCompare(h1, h2, 0) {
		t.Error("different hashes should not compare equal at tolerance 0")
	}
}
