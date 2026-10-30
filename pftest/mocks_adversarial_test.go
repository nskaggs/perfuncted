package pftest_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestFixtureScreenshotterFramePlaybackAndCrop(t *testing.T) {
	red := pftest.SolidImage(4, 3, color.RGBA{R: 255, A: 255})
	green := pftest.SolidImage(4, 3, color.RGBA{G: 255, A: 255})
	s := &pftest.Screenshotter{Frames: []image.Image{red, green}}
	ctx := context.Background()
	for _, want := range []color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {G: 255, A: 255}} {
		img, err := s.Grab(ctx, image.Rect(1, 1, 3, 3))
		if err != nil {
			t.Fatal(err)
		}
		got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA) //nolint:errcheck // RGBAModel.Convert always yields color.RGBA
		if img.Bounds() != image.Rect(0, 0, 2, 2) || got != want {
			t.Fatalf("frame bounds=%v color=%v, want %v", img.Bounds(), got, want)
		}
	}
	s.Reset()
	img, err := s.Grab(ctx, image.Rectangle{})
	if err != nil || img != red {
		t.Fatalf("reset did not restore first frame: %v %v", img, err)
	}
	w, h, err := s.Resolution()
	if err != nil || w != 4 || h != 3 {
		t.Fatalf("resolution: %d,%d,%v", w, h, err)
	}
}

func TestFixtureScreenshotterErrorAndFallback(t *testing.T) {
	sentinel := errors.New("capture failure")
	s := &pftest.Screenshotter{Err: sentinel}
	if _, err := s.Grab(context.Background(), image.Rectangle{}); !errors.Is(err, sentinel) {
		t.Fatalf("capture error: %v", err)
	}
	s.Err = nil
	s.Width = 8
	s.Height = 9
	img, err := s.Grab(context.Background(), image.Rect(99, 99, 100, 100))
	if err != nil || !img.Bounds().Empty() {
		t.Fatalf("expected empty out-of-bounds capture, got %v %v", img.Bounds(), err)
	}
	img, err = s.Grab(context.Background(), image.Rectangle{})
	if err != nil || img.Bounds() != image.Rect(0, 0, 8, 9) {
		t.Fatalf("fallback capture bounds=%v error=%v", img.Bounds(), err)
	}
}

func TestFixtureManagerPlaybackAndErrorPropagation(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	ctx := context.Background()
	manager := &pftest.Manager{Lists: [][]window.Info{{{ID: 1, Title: "First"}}, {{ID: 2, Title: "Second"}}}, Titles: []string{"first", "second"}}
	for _, want := range []string{"First", "Second", "Second"} {
		wins, err := manager.List(ctx)
		if err != nil || len(wins) != 1 || wins[0].Title != want {
			t.Fatalf("list=%v error=%v want %q", wins, err, want)
		}
	}
	for _, want := range []string{"first", "second", "second"} {
		title, err := manager.ActiveTitle(ctx)
		if err != nil || title != want {
			t.Fatalf("active=%q, %v want %q", title, err, want)
		}
	}
	if err := manager.ActivateByID(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manager.Activated, []string{"b"}) {
		t.Fatalf("activated=%v", manager.Activated)
	}
	manager.Reset()
	wins, err := manager.List(ctx)
	if err != nil || len(wins) != 1 || wins[0].Title != "First" {
		t.Fatalf("reset list=%v error=%v", wins, err)
	}
	if len(manager.Activated) != 0 {
		t.Fatalf("reset activations=%v", manager.Activated)
	}
	sentinel := errors.New("backend failure")
	manager.Err = sentinel
	if _, err := manager.List(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("list error=%v", err)
	}
	if _, err := manager.ActiveTitle(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("active error=%v", err)
	}
}

func TestFixtureManagerHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &pftest.Manager{Lists: [][]window.Info{{{ID: 1, Title: "First"}}}}
	if _, err := manager.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled List returned %v, want cancellation", err)
	}
	if err := manager.ActivateByID(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("activate error=%v", err)
	}
	if len(manager.Activated) != 0 {
		t.Fatal("canceled activate recorded a side effect")
	}
}

func TestFixtureScreenshotterHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &pftest.Screenshotter{Width: 8, Height: 8}
	if _, err := s.Grab(ctx, image.Rectangle{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Grab returned %v, want cancellation", err)
	}
}
