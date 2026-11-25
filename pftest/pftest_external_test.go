package pftest_test

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"image/color"
	"reflect"
	"testing"
)

// A failed clipboard write must not silently change the fixture's selection;
// otherwise tests of denied or failed clipboard operations can observe data
// that a real backend did not accept.
func TestFixtureClipboardDeniedSetPreservesPreviousSelection(t *testing.T) {
	denied := errors.New("synthetic clipboard permission denied")
	cb := &pftest.Clipboard{Text: "previous selection", SetErr: denied}
	if err := cb.Set(context.Background(), "secret replacement"); !errors.Is(err, denied) {
		t.Fatalf("denied clipboard write error=%v, want %v", err, denied)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "previous selection" {
		t.Fatalf("denied clipboard write changed selection to %q (read err=%v)", got, err)
	}
	cb.SetErr = nil
	if err := cb.Set(context.Background(), "authorized replacement"); err != nil {
		t.Fatal(err)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "authorized replacement" {
		t.Fatalf("authorized clipboard write lost selection: %q, %v", got, err)
	}
}

func TestFixtureInputterRetainsOrderedInputAndReportsBackendFailure(t *testing.T) { //nolint:gocyclo // The mock contract covers each distinct event operation.
	ctx := context.Background()
	m := &pftest.Inputter{}
	operations := []func() error{
		func() error { return m.KeyDown(ctx, "shift") },
		func() error { return m.Type(ctx, "hello") },
		func() error { return m.TypeLiteral(ctx, "{ctrl+x}") },
		func() error { return m.KeyUp(ctx, "shift") },
		func() error { return m.MouseMove(ctx, -4, 9) },
		func() error { return m.MouseClick(ctx, 20, 30, 1) },
		func() error { return m.MouseDown(ctx, 1) },
		func() error { return m.MouseUp(ctx, 1) },
		func() error { return m.ScrollUp(ctx, 2) },
		func() error { return m.ScrollDown(ctx, 3) },
		func() error { return m.ScrollLeft(ctx, 4) },
		func() error { return m.ScrollRight(ctx, 5) },
	}
	for i, operation := range operations {
		if err := operation(); err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
	want := []string{"down:shift", "type:hello", "type-literal:{ctrl+x}", "up:shift", "move:-4,9", "click:20,30", "mousedown", "mouseup", "scroll-up:2", "scroll-down:3", "scroll-left:4", "scroll-right:5"}
	if !reflect.DeepEqual(m.Calls, want) {
		t.Fatalf("recorded input %v want %v", m.Calls, want)
	}
	if got := m.Typed(); got != "hello{ctrl+x}" {
		t.Fatalf("literal typed text lost: %q", got)
	}
	x, y, err := m.PointerLocation(ctx)
	if err != nil || x != 0 || y != 0 {
		t.Fatalf("pointer=(%d,%d), %v", x, y, err)
	}
	coord, err := m.PointerCoordinateSpace(ctx)
	if err != nil || coord.Kind != input.CoordinateSpaceLogical || coord.ScaleX != 1 || coord.ScaleY != 1 {
		t.Fatalf("coordinate metadata = %+v error=%v", coord, err)
	}
	if err := m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m.Reset()
	if len(m.Calls) != 0 || m.Typed() != "" {
		t.Fatal("reset kept old input events")
	}
	expected := errors.New("no input")
	m.Err = expected
	if err := m.Type(ctx, "cannot type"); !errors.Is(err, expected) {
		t.Fatalf("type error = %v", err)
	}
	if _, _, err := m.PointerLocation(ctx); !errors.Is(err, expected) {
		t.Fatalf("pointer error = %v", err)
	}
	if _, err := m.PointerCoordinateSpace(ctx); !errors.Is(err, expected) {
		t.Fatalf("coordinate error = %v", err)
	}
	if err := m.Sync(ctx); !errors.Is(err, expected) {
		t.Fatalf("sync error = %v", err)
	}
}

func TestFixtureManagerControlAndStableIDLookup(t *testing.T) { //nolint:gocyclo // Each control operation is independently tested for error forwarding.
	ctx := context.Background()
	fixture := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "window-1", Title: "Editor", Active: true}}}}
	info, err := fixture.InfoByID(ctx, "window-1")
	if err != nil || info.Title != "Editor" {
		t.Fatalf("lookup=%+v err=%v", info, err)
	}
	if _, err := fixture.InfoByID(ctx, "wrong"); !errors.Is(err, window.ErrWindowNotFound) {
		t.Fatalf("missing window error = %v", err)
	}
	if matched, err := fixture.FindByTitle(ctx, "dit"); err != nil || matched.Title != "Editor" {
		t.Fatalf("find by substring = %+v, %v", matched, err)
	}
	originalError := errors.New("synthetic window failure")
	fixture.Err = originalError
	operations := []func() error{
		func() error { return fixture.MoveByID(ctx, "window-1", 5, 6) },
		func() error { return fixture.ResizeByID(ctx, "window-1", 50, 60) },
		func() error { return fixture.CloseWindowByID(ctx, "window-1") },
		func() error { return fixture.MinimizeByID(ctx, "window-1") },
		func() error { return fixture.MaximizeByID(ctx, "window-1") },
		func() error { return fixture.FullscreenByID(ctx, "window-1") },
		func() error { return fixture.UnfullscreenByID(ctx, "window-1") },
		func() error { return fixture.RestoreByID(ctx, "window-1") },
	}
	for i, operation := range operations {
		if err := operation(); !errors.Is(err, originalError) {
			t.Fatalf("control operation %d lost error: %v", i, err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureClipboardProvidesDeterministicErrorPath(t *testing.T) {
	ctx := context.Background()
	c := &pftest.Clipboard{Text: "before"}
	if got, err := c.Get(ctx); err != nil || got != "before" {
		t.Fatalf("initial clipboard=%q, %v", got, err)
	}
	if err := c.Set(ctx, "after"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(ctx); err != nil || got != "after" {
		t.Fatalf("updated clipboard=%q, %v", got, err)
	}
	sentinel := errors.New("clipboard read failed")
	c.GetErr = sentinel
	if _, err := c.Get(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("get error=%v", err)
	}
	c.SetErr = sentinel
	if err := c.Set(ctx, "failure"); !errors.Is(err, sentinel) {
		t.Fatalf("set error=%v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureScreenHashMatchesPixelHashAndFailure(t *testing.T) {
	ctx := context.Background()
	frame := pftest.SolidImage(8, 8, color.RGBA{G: 25, A: 255})
	s := &pftest.Screenshotter{Frames: []image.Image{frame}}
	if !s.CanonicalHashing() {
		t.Fatal("mock does not declare canonical pixel hashing")
	}
	whole, err := s.GrabFullHash(ctx)
	if err != nil || whole != find.PixelHash(frame, nil) {
		t.Fatalf("full hash mismatch: %d, %v", whole, err)
	}
	region := image.Rect(2, 3, 5, 6)
	clipped, err := s.Grab(ctx, region)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := s.GrabRegionHash(ctx, region)
	if err != nil || hash != find.PixelHash(clipped, nil) {
		t.Fatalf("region hash mismatch: %d, %v", hash, err)
	}
	s.Err = errors.New("capture error")
	if _, err := s.GrabFullHash(ctx); !errors.Is(err, s.Err) {
		t.Fatalf("expected full capture failure: %v", err)
	}
	if _, err := s.GrabRegionHash(ctx, region); !errors.Is(err, s.Err) {
		t.Fatalf("expected region capture failure: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

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

type outputFixtureLifetime struct {
	infos  []output.Info
	err    error
	closed bool
}

func (o *outputFixtureLifetime) List(ctx context.Context) ([]output.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o.infos, o.err
}
func (o *outputFixtureLifetime) Close() error { o.closed = true; return nil }

func TestNewWithOutputsOwnsOutputDiscoveryFailureAndTeardown(t *testing.T) {
	want := []output.Info{{Name: "Synthetic-A", Geometry: output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080}, Available: true}, {Name: "Synthetic-B", Geometry: output.Geometry{X: 0, Y: 0, W: 2560, H: 1440}, Available: true}}
	backend := &outputFixtureLifetime{infos: want}
	session := pftest.NewWithOutputs(nil, nil, nil, backend, nil)
	ctx := context.Background()
	got, err := session.Outputs.List(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-output fixture lost physical coordinates: %+v %v", got, err)
	}
	failure := errors.New("synthetic display disconnected")
	backend.err = failure
	if _, err := session.Outputs.List(ctx); !errors.Is(err, failure) {
		t.Fatalf("output disconnect masked: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !backend.closed {
		t.Fatal("owned output backend was not closed")
	}
	if _, err := session.Outputs.List(ctx); err == nil {
		t.Fatal("closed output fixture still allowed discovery")
	}
}

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
		return
	}
	pftest.CaptureOnFailure(t, pf, "")

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
		return
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
	if c := color.RGBAModel.Convert(got.At(1, 1)).(color.RGBA); c != want { //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
		t.Fatalf("pixel = %+v, want %+v", c, want)
	}
}
