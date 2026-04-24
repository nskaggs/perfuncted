package perfuncted_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestNewAssemblesAllBackends(t *testing.T) {
	// Use pftest to provide mocks for all backends.
	sc := &pftest.Screenshotter{Width: 100, Height: 100}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	cb := &pftest.Clipboard{}

	pf := pftest.New(sc, inp, mgr, cb)
	defer pf.Close()

	if pf.Screen.Screenshotter != sc {
		t.Error("pf.Screen.Screenshotter not correctly assigned")
	}
	// pf.Input uses InputBundle which wraps inp.
	if err := pf.Input.KeyTap("a"); err != nil {
		t.Errorf("KeyTap: %v", err)
	}
	if err := pf.Input.PressCombo("ctrl+c"); err != nil {
		t.Errorf("PressCombo: %v", err)
	}
	if pf.Window.Manager != mgr {
		t.Error("pf.Window.Manager not correctly assigned")
	}
	if pf.Clipboard.Clipboard != cb {
		t.Error("pf.Clipboard.Clipboard not correctly assigned")
	}
}

type tapErrInputter struct {
	pftest.Inputter
}

type closeErrScreen struct {
	pftest.Screenshotter
	err error
}

func (s *closeErrScreen) Close() error { return s.err }

type closeErrInput struct {
	pftest.Inputter
	err error
}

func (i *closeErrInput) Close() error { return i.err }

type closeErrWindow struct {
	pftest.Manager
	err error
}

func (w *closeErrWindow) Close() error { return w.err }

type closeErrClipboard struct {
	pftest.Clipboard
	err error
}

func (c *closeErrClipboard) Close() error { return c.err }

func (m *tapErrInputter) KeyTap(ctx context.Context, key string) error {
	return errors.New("tap error")
}

func (m *tapErrInputter) PressCombo(ctx context.Context, combo string) error {
	return nil
}

func TestInputBundleErrors(t *testing.T) {
	inp := &tapErrInputter{}
	pf := pftest.New(nil, inp, nil, nil)

	err := pf.Input.KeyTap("a")
	if err == nil || err.Error() != "tap error" {
		t.Errorf("expected 'tap error', got %v", err)
	}
}

func TestCloseJoinsErrors(t *testing.T) {
	screenErr := errors.New("screen close failed")
	inputErr := errors.New("input close failed")
	windowErr := errors.New("window close failed")
	clipboardErr := errors.New("clipboard close failed")

	pf := &perfuncted.Perfuncted{
		Screen: perfuncted.ScreenBundle{Screenshotter: &closeErrScreen{err: screenErr}},
		Input:  perfuncted.InputBundle{Inputter: &closeErrInput{err: inputErr}},
		Window: perfuncted.WindowBundle{Manager: &closeErrWindow{err: windowErr}},
		Clipboard: perfuncted.ClipboardBundle{
			Clipboard: &closeErrClipboard{err: clipboardErr},
		},
	}

	err := pf.Close()
	if !errors.Is(err, screenErr) || !errors.Is(err, inputErr) ||
		!errors.Is(err, windowErr) || !errors.Is(err, clipboardErr) {
		t.Fatalf("close error %v does not include all component errors", err)
	}
}

func TestWindowBundle(t *testing.T) {
	mgr := &pftest.Manager{
		Lists: [][]window.Info{
			{{ID: 1, Title: "Firefox"}},
		},
	}
	pf := pftest.New(nil, nil, mgr, nil)

	wins, err := pf.Window.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(wins) != 1 || wins[0].Title != "Firefox" {
		t.Errorf("unexpected list: %v", wins)
	}

	if err := pf.Window.Activate("Firefox"); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Activated) != 1 || mgr.Activated[0] != "Firefox" {
		t.Errorf("unexpected activated: %v", mgr.Activated)
	}
}

func TestScreenBundleHashing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	sc := &pftest.Screenshotter{Frames: []image.Image{img}}
	pf := pftest.New(sc, nil, nil, nil)

	h1, err := pf.Screen.GrabHash(image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatal(err)
	}
	if h1 == 0 {
		t.Error("expected non-zero hash")
	}

	// GrabFullHash
	h2, err := pf.Screen.GrabFullHash()
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h1 {
		t.Errorf("expected same hash for full screen, got %08x vs %08x", h1, h2)
	}
}

func TestWaitForVisibleChange(t *testing.T) {
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2.Set(5, 5, color.RGBA{R: 255, A: 255})

	sc := &pftest.Screenshotter{
		Frames: []image.Image{img1, img1, img2, img2, img2},
	}
	pf := pftest.New(sc, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForVisibleChangeContext(ctx, image.Rect(0, 0, 10, 10), 10*time.Millisecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}

func TestWaitForSettle(t *testing.T) {
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2.Set(5, 5, color.RGBA{R: 255, A: 255})

	sc := &pftest.Screenshotter{
		Frames: []image.Image{img1, img1, img2, img2, img2, img2},
	}
	pf := pftest.New(sc, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := pf.Screen.WaitForSettleContext(ctx, image.Rect(0, 0, 10, 10), func() {
		// simulate action that causes change
	}, 3, 10*time.Millisecond)

	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}
