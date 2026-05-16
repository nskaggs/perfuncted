package pftest_test

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestNewAssemblesAllBackends(t *testing.T) {
	sc := &pftest.Screenshotter{
		Frames: []image.Image{pftest.SolidImage(8, 8, color.RGBA{255, 0, 0, 255})},
	}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	cb := &pftest.Clipboard{Text: "hi"}

	pf := pftest.New(sc, inp, mgr, cb)
	if pf == nil {
		t.Fatal("New returned nil")
	}

	// Exercise each bundle through the assembled Perfuncted.
	if _, _, err := pf.Screen.Resolution(context.Background()); err != nil {
		t.Errorf("Resolution: %v", err)
	}
	if err := pf.Input.Type(context.Background(), "{enter}"); err != nil {
		t.Errorf("Type: %v", err)
	}
	if got, err := pf.Clipboard.Get(context.Background()); err != nil || got != "hi" {
		t.Errorf("Clipboard.Get: %q, %v", got, err)
	}
}

func TestNewNilBackends(t *testing.T) {
	pf := pftest.New(nil, nil, nil, nil)
	if pf == nil {
		t.Fatal("New returned nil")
	}
	// All bundles are zero-valued; operations should return errors, not panic.
	if _, _, err := pf.Screen.Resolution(context.Background()); err == nil {
		t.Error("expected error for nil screen")
	}
	if err := pf.Input.Type(context.Background(), "{ctrl+s}"); err == nil {
		t.Error("expected error for nil inputter")
	}
	if _, err := pf.Clipboard.Get(context.Background()); err == nil {
		t.Error("expected error for nil clipboard")
	}
}

func TestScreenshotterZeroOrigin(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 14, 24))
	want := color.RGBA{R: 3, G: 4, B: 5, A: 255}
	img.SetRGBA(12, 22, want)
	sc := &pftest.Screenshotter{Frames: []image.Image{img}, ZeroOrigin: true}

	got, err := sc.Grab(context.Background(), image.Rect(11, 21, 13, 23))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v, want zero-origin 2x2", got.Bounds())
	}
	if c := color.RGBAModel.Convert(got.At(1, 1)).(color.RGBA); c != want {
		t.Fatalf("pixel = %+v, want %+v", c, want)
	}
}
