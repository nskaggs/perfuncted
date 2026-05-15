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
	t.Parallel()
	// Use pftest to provide mocks for all backends.
	sc := &pftest.Screenshotter{Width: 100, Height: 100}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	cb := &pftest.Clipboard{}
	ctx := context.Background()

	pf := pftest.New(sc, inp, mgr, cb)
	defer pf.Close()

	if pf.Screen.Screenshotter != sc {
		t.Error("pf.Screen.Screenshotter not correctly assigned")
	}
	// pf.Input uses InputBundle which wraps inp.
	if err := pf.Input.Type(ctx, "a"); err != nil {
		t.Errorf("Type: %v", err)
	}
	if err := pf.Input.Type(ctx, "^c"); err != nil {
		t.Errorf("Type ctrl+c: %v", err)
	}
	if pf.Window.Manager != mgr {
		t.Error("pf.Window.Manager not correctly assigned")
	}
	if pf.Clipboard.Clipboard != cb {
		t.Error("pf.Clipboard.Clipboard not correctly assigned")
	}
}

func TestBundleSmoke(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	pf := pftest.New(sc, inp, mgr, nil)
	ctx := context.Background()

	t.Run("Input", func(t *testing.T) {
		// TODO: ModifierDown and ModifierUp methods are not yet implemented
		// if err := pf.Input.ModifierDown("ctrl"); err != nil {
		// 	t.Fatal(err)
		// }
		// if err := pf.Input.ModifierUp("ctrl"); err != nil {
		// 	t.Fatal(err)
		// }
		if err := pf.Input.Type(ctx, "hello"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.MouseClick(ctx, 10, 20, 1); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DoubleClick(ctx, 10, 20); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DragAndDrop(ctx, 0, 0, 100, 100); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Screen", func(t *testing.T) {
		if _, err := pf.Screen.GetPixel(ctx, 5, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := pf.Screen.GetMultiplePixels(ctx, []image.Point{{1, 1}}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Window", func(t *testing.T) {
		_ = pf.Window.Resize(ctx, "Firefox", 800, 600)
		_ = pf.Window.Minimize(ctx, "Firefox")
		_ = pf.Window.Maximize(ctx, "Firefox")
		_ = pf.Window.Restore(ctx, "Firefox")
	})
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

type contextKey struct{}

type contextSpyInputter struct {
	pftest.Inputter
	ctxValue any
}

func (s *contextSpyInputter) Type(ctx context.Context, text string) error {
	s.ctxValue = ctx.Value(contextKey{})
	return nil
}

type contextSpyManager struct {
	pftest.Manager
	ctxValue any
}

func (m *contextSpyManager) List(ctx context.Context) ([]window.Info, error) {
	m.ctxValue = ctx.Value(contextKey{})
	return []window.Info{{Title: "Firefox"}}, nil
}

type contextSpyClipboard struct {
	pftest.Clipboard
	setValue any
	getValue any
}

func (c *contextSpyClipboard) Get(ctx context.Context) (string, error) {
	c.getValue = ctx.Value(contextKey{})
	return c.Text, nil
}

func (c *contextSpyClipboard) Set(ctx context.Context, text string) error {
	c.setValue = ctx.Value(contextKey{})
	c.Text = text
	return nil
}

type contextSpyScreenshotter struct {
	pftest.Screenshotter
	ctxValue any
}

func (s *contextSpyScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.ctxValue = ctx.Value(contextKey{})
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (m *tapErrInputter) Type(ctx context.Context, s string) error {
	return errors.New("type error")
}

func TestInputBundleErrors(t *testing.T) {
	t.Parallel()
	inp := &tapErrInputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	err := pf.Input.Type(ctx, "a")
	if err == nil || err.Error() != "type error" {
		t.Errorf("expected 'type error', got %v", err)
	}
}

func TestBundleMethodsPropagateContext(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), contextKey{}, "token")
	inp := &contextSpyInputter{}
	mgr := &contextSpyManager{}
	cb := &contextSpyClipboard{}
	sc := &contextSpyScreenshotter{}
	pf := &perfuncted.Perfuncted{
		Screen:    perfuncted.ScreenBundle{Screenshotter: sc},
		Input:     perfuncted.InputBundle{Inputter: inp},
		Window:    perfuncted.WindowBundle{Manager: mgr},
		Clipboard: perfuncted.ClipboardBundle{Clipboard: cb},
	}

	if err := pf.Input.Type(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.Window.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pf.Clipboard.Set(ctx, "clipboard"); err != nil {
		t.Fatal(err)
	}
	if got, err := pf.Clipboard.Get(ctx); err != nil || got != "clipboard" {
		t.Fatalf("Clipboard.Get = %q, %v", got, err)
	}
	if _, err := pf.Screen.GetPixel(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}

	if inp.ctxValue != "token" {
		t.Fatalf("input ctx = %v, want token", inp.ctxValue)
	}
	if mgr.ctxValue != "token" {
		t.Fatalf("window ctx = %v, want token", mgr.ctxValue)
	}
	if cb.setValue != "token" || cb.getValue != "token" {
		t.Fatalf("clipboard ctx = set:%v get:%v, want token", cb.setValue, cb.getValue)
	}
	if sc.ctxValue != "token" {
		t.Fatalf("screen ctx = %v, want token", sc.ctxValue)
	}
}

func TestRetryZeroPoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	calls := 0
	err := perfuncted.Retry(ctx, 0, func() error {
		calls++
		return errors.New("try again")
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if calls == 0 {
		t.Fatal("expected retry function to be called")
	}
}

func TestCloseJoinsErrors(t *testing.T) {
	t.Parallel()
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
	ctx := context.Background()

	wins, err := pf.Window.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wins) != 1 || wins[0].Title != "Firefox" {
		t.Errorf("unexpected list: %v", wins)
	}

	if err := pf.Window.Activate(ctx, "Firefox"); err != nil {
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
	ctx := context.Background()

	h1, err := pf.Screen.GrabRegionHash(ctx, image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatal(err)
	}
	if h1 == 0 {
		t.Error("expected non-zero hash")
	}

	// GrabFullHash - equivalent to GrabRegionHash with empty rect
	h2, err := pf.Screen.GrabFullHash(ctx)
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

	h, err := pf.Screen.WaitForSettle(ctx, image.Rect(0, 0, 10, 10), func() {}, 2, 10*time.Millisecond)
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

	h, err := pf.Screen.WaitForSettle(ctx, image.Rect(0, 0, 10, 10), func() {
		// simulate action that causes change
	}, 3, 10*time.Millisecond)

	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}
