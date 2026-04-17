package perfuncted_test

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestClipboardPasteWithInput(t *testing.T) {
	cb := perfuncted.ClipboardBundle{Clipboard: &pftest.Clipboard{}}
	inp := &pftest.Inputter{}
	if err := cb.PasteWithInput(inp, "hello"); err != nil {
		t.Fatal(err)
	}
	// Verify clipboard text set
	if c, ok := cb.Clipboard.(*pftest.Clipboard); ok {
		if c.Text != "hello" {
			t.Fatalf("clipboard text = %q", c.Text)
		}
	} else {
		t.Fatal("unexpected clipboard backend type")
	}
	// Verify paste key sequence
	want := []string{"down:ctrl", "tap:v", "up:ctrl"}
	if len(inp.Calls) != len(want) {
		t.Fatalf("want %v, got %v", want, inp.Calls)
	}
	for i := range want {
		if inp.Calls[i] != want[i] {
			t.Fatalf("call[%d] want %q got %q", i, want[i], inp.Calls[i])
		}
	}
}

func TestUtilWaitForPixelColorAndImageAndHash(t *testing.T) {
	// WaitForPixelColor: frame 0 black, frame 1 red
	black := pftest.SolidImage(2, 2, color.RGBA{A: 255})
	red := pftest.SolidImage(2, 2, color.RGBA{R: 255, A: 255})
	sc := &pftest.Screenshotter{Frames: []image.Image{black, red, red}}
	ok, err := util.WaitForPixelColor(sc, image.Rect(0, 0, 2, 2), color.RGBA{R: 255, A: 255}, 0, 500*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("WaitForPixelColor failed: %v, ok=%v", err, ok)
	}

	// WaitForImage: create a 4x4 image with a 2x2 template at (1,1)
	full := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			full.SetRGBA(x, y, color.RGBA{A: 255})
		}
	}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			c := color.RGBA{R: 10, G: 20, B: 30, A: 255}
			full.SetRGBA(1+x, 1+y, c)
			ref.SetRGBA(x, y, c)
		}
	}
	sc2 := &pftest.Screenshotter{Frames: []image.Image{full}}
	res, err := util.WaitForImage(sc2, ref, "exact", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Rect != image.Rect(1, 1, 3, 3) {
		t.Fatalf("unexpected match results: %#v", res)
	}

	// ImageHashCompare: identical images
	h1 := find.PixelHash(full, nil)
	h2 := find.PixelHash(full, nil)
	if !util.ImageHashCompare(h1, h2, 0) {
		t.Fatalf("expected identical hashes to compare true")
	}
	// Different images should fail with zero tolerance
	h3 := find.PixelHash(red, nil)
	if util.ImageHashCompare(h1, h3, 0) {
		t.Fatalf("expected different hashes to compare false")
	}
}
