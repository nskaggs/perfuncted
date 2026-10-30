package pftest_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

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
