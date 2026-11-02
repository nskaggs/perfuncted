package pftest_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestFixtureScreenEmptyPlaybackAndScaledImageContracts(t *testing.T) {
	ctx := context.Background()
	blank := &pftest.Screenshotter{}
	full, err := blank.Grab(ctx, image.Rectangle{})
	if err != nil || full.Bounds() != image.Rect(0, 0, 1, 1) {
		t.Fatalf("default image=%v err=%v", full, err)
	}
	part, err := blank.Grab(ctx, image.Rect(0, 0, 1, 1))
	if err != nil || part.Bounds() != image.Rect(0, 0, 1, 1) {
		t.Fatalf("fallback region=%v err=%v", part, err)
	}
	w, h, err := blank.Resolution()
	if err != nil || w != 0 || h != 0 {
		t.Fatalf("zero screen resolution=%dx%d err=%v", w, h, err)
	}
}

func TestFixtureScreenRebasesFramesAndPreservesCustomCallbackErrors(t *testing.T) {
	ctx := context.Background()
	colored := pftest.SolidImage(7, 4, color.RGBA{R: 244, A: 255})
	replay := &pftest.Screenshotter{Frames: []image.Image{colored}}
	img, err := replay.Grab(ctx, image.Rect(999, 999, 1000, 1000))
	if err != nil || !img.Bounds().Empty() {
		t.Fatalf("out of bounds image=%v err=%v", img, err)
	}
	replay.ZeroOrigin = true
	img, err = replay.Grab(ctx, image.Rectangle{})
	if err != nil || img.Bounds() != image.Rect(0, 0, 7, 4) || img == colored {
		t.Fatalf("rebased image=%v err=%v", img, err)
	}
	sentinel := errors.New("untrusted test fixture")
	custom := &pftest.Screenshotter{GrabFunc: func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		if rect != image.Rect(3, 4, 5, 6) {
			t.Fatalf("callback lost rect=%v", rect)
		}
		return nil, sentinel
	}}
	if img, err = custom.Grab(ctx, image.Rect(3, 4, 5, 6)); img != nil || !errors.Is(err, sentinel) {
		t.Fatalf("custom capture=%v err=%v", img, err)
	}
}

func TestFixtureWindowIteratorCancellationAndEarlyExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	windows := []window.Info{{NativeID: "a", Title: "first"}, {NativeID: "b", Title: "second"}}
	manager := &pftest.Manager{Lists: [][]window.Info{windows}}
	observed := 0
	for win, err := range manager.IterateWindows(ctx) {
		if err != nil || win.NativeID != "a" {
			t.Fatalf("first window=%+v err=%v", win, err)
		}
		observed++
		break
	}
	if observed != 1 {
		t.Fatalf("early exit yielded %d", observed)
	}
}

func TestFixtureWindowIteratorStopsAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	windows := []window.Info{{NativeID: "a", Title: "first"}, {NativeID: "b", Title: "second"}}
	manager := &pftest.Manager{Lists: [][]window.Info{windows}}
	results := 0
	var termination error
	for win, err := range manager.IterateWindows(ctx) {
		if err != nil {
			termination = err
			break
		}
		if win.NativeID != "a" {
			t.Fatalf("untrusted unexpected window %+v", win)
		}
		results++
		cancel()
	}
	if results != 1 || !errors.Is(termination, context.Canceled) {
		t.Fatalf("iterator after cancellation: results=%d err=%v", results, termination)
	}
}

func TestFixtureEmptyWindowAndDeniedControlOperations(t *testing.T) {
	if title, err := (&pftest.Manager{}).ActiveTitle(context.Background()); title != "" || err != nil {
		t.Fatalf("empty title=%q err=%v", title, err)
	}
	empty := &pftest.Manager{}
	seen := false
	for _, err := range empty.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatalf("empty fixture yielded error: %v", err)
		}
		seen = true
	}
	if seen {
		t.Fatal("empty fixture yielded windows")
	}
	failed := &pftest.Manager{Err: errors.New("window listing denied")}
	if _, err := failed.InfoByID(context.Background(), "anything"); !errors.Is(err, failed.Err) {
		t.Fatalf("InfoByID did not propagate listing failure: %v", err)
	}
	if err := failed.ActivateByID(context.Background(), "blocked"); !errors.Is(err, failed.Err) || len(failed.Activated) != 0 {
		t.Fatalf("denied activation recorded side effect: %v %v", err, failed.Activated)
	}
}
