package perfuncted_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/window"
)

// ── mockScreenshotter ─────────────────────────────────────────────────────────

// mockScreenshotter returns a fixed-size solid-colour image, cycling through
// frames on each Grab call (used to simulate screen changes).
type mockScreenshotter struct {
	w, h   int
	frames []color.RGBA // one per Grab call; last repeated
	idx    int
}

func (m *mockScreenshotter) Grab(rect image.Rectangle) (image.Image, error) {
	c := m.frames[m.idx]
	if m.idx < len(m.frames)-1 {
		m.idx++
	}
	img := image.NewRGBA(image.Rect(0, 0, m.w, m.h))
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			img.Set(x, y, c)
		}
	}
	return img, nil
}
func (m *mockScreenshotter) Close() error { return nil }
func (m *mockScreenshotter) Resolution() (int, int, error) {
	return m.w, m.h, nil
}

// mockInputter records all calls made to it.
type mockInputter struct {
	calls []string
	err   error // returned on all calls when non-nil
}

func (m *mockInputter) KeyDown(key string) error {
	m.calls = append(m.calls, "down:"+key)
	return m.err
}
func (m *mockInputter) KeyUp(key string) error {
	m.calls = append(m.calls, "up:"+key)
	return m.err
}
func (m *mockInputter) KeyTap(key string) error {
	m.calls = append(m.calls, "tap:"+key)
	return m.err
}
func (m *mockInputter) Type(s string) error {
	m.calls = append(m.calls, "type:"+s)
	return m.err
}
func (m *mockInputter) MouseMove(x, y int) error {
	m.calls = append(m.calls, "move")
	return m.err
}
func (m *mockInputter) MouseClick(x, y, button int) error {
	m.calls = append(m.calls, "click")
	return m.err
}
func (m *mockInputter) MouseDown(button int) error {
	m.calls = append(m.calls, "mousedown")
	return m.err
}
func (m *mockInputter) MouseUp(button int) error {
	m.calls = append(m.calls, "mouseup")
	return m.err
}
func (m *mockInputter) ScrollUp(n int) error    { return m.err }
func (m *mockInputter) ScrollDown(n int) error  { return m.err }
func (m *mockInputter) ScrollLeft(n int) error  { return m.err }
func (m *mockInputter) ScrollRight(n int) error { return m.err }
func (m *mockInputter) Close() error            { return nil }

// mockManager records calls and returns preset window lists.
type mockManager struct {
	lists     [][]window.Info // successive List() calls return these in order
	listIdx   int
	titles    []string // successive ActiveTitle() calls return these in order
	titleIdx  int
	activated []string
	err       error
}

func (m *mockManager) List() ([]window.Info, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.listIdx >= len(m.lists) {
		return m.lists[len(m.lists)-1], nil
	}
	r := m.lists[m.listIdx]
	m.listIdx++
	return r, nil
}
func (m *mockManager) Activate(title string) error {
	m.activated = append(m.activated, title)
	return m.err
}
func (m *mockManager) ActiveTitle() (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if m.titleIdx >= len(m.titles) {
		return m.titles[len(m.titles)-1], nil
	}
	t := m.titles[m.titleIdx]
	m.titleIdx++
	return t, nil
}
func (m *mockManager) Move(title string, x, y int) error   { return m.err }
func (m *mockManager) Resize(title string, w, h int) error { return m.err }
func (m *mockManager) CloseWindow(title string) error      { return m.err }
func (m *mockManager) Minimize(title string) error         { return m.err }
func (m *mockManager) Maximize(title string) error         { return m.err }
func (m *mockManager) Close() error                        { return nil }

// ── InputBundle tests ─────────────────────────────────────────────────────────

func TestPressComboSingleKey(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.PressCombo("return"); err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 1 || m.calls[0] != "tap:return" {
		t.Errorf("want [tap:return], got %v", m.calls)
	}
}

func TestPressComboWithModifiers(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.PressCombo("ctrl+s"); err != nil {
		t.Fatal(err)
	}
	want := []string{"down:ctrl", "tap:s", "up:ctrl"}
	if len(m.calls) != len(want) {
		t.Fatalf("want %v, got %v", want, m.calls)
	}
	for i, c := range want {
		if m.calls[i] != c {
			t.Errorf("call[%d]: want %q, got %q", i, c, m.calls[i])
		}
	}
}

func TestPressComboMultipleModifiers(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.PressCombo("ctrl+shift+z"); err != nil {
		t.Fatal(err)
	}
	// modifiers held in order, released in reverse
	want := []string{"down:ctrl", "down:shift", "tap:z", "up:shift", "up:ctrl"}
	if len(m.calls) != len(want) {
		t.Fatalf("want %v, got %v", want, m.calls)
	}
	for i, c := range want {
		if m.calls[i] != c {
			t.Errorf("call[%d]: want %q, got %q", i, c, m.calls[i])
		}
	}
}

func TestPressComboReleasesOnTapError(t *testing.T) {
	callCount := 0
	m := &mockInputter{}
	// override: fail only on KeyTap
	inp := perfuncted.InputBundle{Inputter: &tapErrInputter{mock: m, failAfter: &callCount}}
	// Should still attempt to release modifiers
	_ = inp.PressCombo("ctrl+s")
	// down:ctrl must be followed by up:ctrl
	hasDown := false
	hasUp := false
	for _, c := range m.calls {
		if c == "down:ctrl" {
			hasDown = true
		}
		if c == "up:ctrl" {
			hasUp = true
		}
	}
	if !hasDown {
		t.Error("expected down:ctrl to be called")
	}
	if !hasUp {
		t.Error("expected up:ctrl to be called even after tap error")
	}
}

func TestDoubleClick(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.DoubleClick(10, 20); err != nil {
		t.Fatal(err)
	}
	// DoubleClick = click + sleep + click
	clicks := 0
	for _, c := range m.calls {
		if c == "click" {
			clicks++
		}
	}
	if clicks != 2 {
		t.Errorf("want 2 clicks, got %d (calls: %v)", clicks, m.calls)
	}
}

func TestDragAndDrop(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	if err := inp.DragAndDrop(0, 0, 100, 100); err != nil {
		t.Fatal(err)
	}
	want := []string{"move", "mousedown", "move", "mouseup"}
	if len(m.calls) != len(want) {
		t.Fatalf("want %v, got %v", want, m.calls)
	}
	for i, c := range want {
		if m.calls[i] != c {
			t.Errorf("call[%d]: want %q, got %q", i, c, m.calls[i])
		}
	}
}

func TestClickRectCenter(t *testing.T) {
	m := &mockInputter{}
	inp := perfuncted.InputBundle{Inputter: m}
	rect := image.Rect(10, 20, 110, 120) // center = (60, 70)
	if err := inp.ClickRectCenter(rect); err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 1 || m.calls[0] != "click" {
		t.Errorf("want [click], got %v", m.calls)
	}
}

func TestPressComboNilInputter(t *testing.T) {
	inp := perfuncted.InputBundle{}
	err := inp.PressCombo("ctrl+s")
	if err == nil {
		t.Error("expected error for nil inputter")
	}
}

// ── WindowBundle tests ────────────────────────────────────────────────────────

func TestActivateByMatch(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{
			{Title: "Firefox — Mozilla"},
			{Title: "Terminal"},
		}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	if err := w.ActivateBy("firefox"); err != nil {
		t.Fatal(err)
	}
	if len(mgr.activated) != 1 || mgr.activated[0] != "Firefox — Mozilla" {
		t.Errorf("wrong activation: %v", mgr.activated)
	}
}

func TestActivateByNoMatch(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{
			{Title: "Terminal"},
		}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	err := w.ActivateBy("firefox")
	if err == nil {
		t.Error("expected error for no match")
	}
}

func TestWindowWaitForAppears(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{
			{},                             // first poll: empty
			{{Title: "KWrite — Untitled"}}, // second poll: window appeared
		},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := w.WaitFor(ctx, "kwrite", 1*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "KWrite — Untitled" {
		t.Errorf("unexpected title: %q", info.Title)
	}
}

func TestWindowWaitForTimeout(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := w.WaitFor(ctx, "nonexistent", 5*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestWindowWaitForCloseDisappears(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{
			{{Title: "KWrite — Untitled"}}, // first: still open
			{},                             // second: gone
		},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.WaitForClose(ctx, "kwrite", 1*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestWindowWaitForCloseTimeout(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{{Title: "KWrite — Untitled"}}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := w.WaitForClose(ctx, "kwrite", 5*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestWindowWaitForTitleChange(t *testing.T) {
	mgr := &mockManager{
		titles: []string{"Before", "Before", "After"},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	title, err := w.WaitForTitleChange(ctx, 1*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if title != "After" {
		t.Errorf("want %q, got %q", "After", title)
	}
}

func TestWindowWaitForTitleChangeTimeout(t *testing.T) {
	mgr := &mockManager{
		titles: []string{"Same"},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := w.WaitForTitleChange(ctx, 5*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

// tapErrInputter wraps mockInputter and fails on KeyTap.
type tapErrInputter struct {
	mock      *mockInputter
	failAfter *int
}

func (t *tapErrInputter) KeyDown(key string) error     { return t.mock.KeyDown(key) }
func (t *tapErrInputter) KeyUp(key string) error       { return t.mock.KeyUp(key) }
func (t *tapErrInputter) KeyTap(key string) error      { return errors.New("tap failed") }
func (t *tapErrInputter) Type(s string) error          { return t.mock.Type(s) }
func (t *tapErrInputter) MouseMove(x, y int) error     { return t.mock.MouseMove(x, y) }
func (t *tapErrInputter) MouseClick(x, y, b int) error { return t.mock.MouseClick(x, y, b) }
func (t *tapErrInputter) MouseDown(b int) error        { return t.mock.MouseDown(b) }
func (t *tapErrInputter) MouseUp(b int) error          { return t.mock.MouseUp(b) }
func (t *tapErrInputter) ScrollUp(n int) error         { return nil }
func (t *tapErrInputter) ScrollDown(n int) error       { return nil }
func (t *tapErrInputter) ScrollLeft(n int) error       { return nil }
func (t *tapErrInputter) ScrollRight(n int) error      { return nil }
func (t *tapErrInputter) Close() error                 { return nil }

// ── ScreenBundle tests ────────────────────────────────────────────────────────

func newScreen(w, h int, frames ...color.RGBA) perfuncted.ScreenBundle {
	return perfuncted.ScreenBundle{Screenshotter: &mockScreenshotter{w: w, h: h, frames: frames}}
}

func TestGrabFull(t *testing.T) {
	s := newScreen(320, 240, color.RGBA{255, 0, 0, 255})
	img, err := s.GrabFull()
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 320 || b.Dy() != 240 {
		t.Errorf("want 320x240, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestGrabFullHash(t *testing.T) {
	s := newScreen(320, 240, color.RGBA{255, 0, 0, 255})
	h, err := s.GrabFullHash()
	if err != nil {
		t.Fatal(err)
	}
	if h == 0 {
		t.Error("expected non-zero hash")
	}
}

func TestGrabFullHashDiffersForDifferentContent(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	h1, _ := newScreen(320, 240, red).GrabFullHash()
	h2, _ := newScreen(320, 240, blue).GrabFullHash()
	if h1 == h2 {
		t.Error("expected different hashes for different colours")
	}
}

func TestWaitForVisibleChangeDetectsChange(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	// frame 0: initial (red); frames 1-N: blue (changed and stable)
	frames := []color.RGBA{red, blue, blue, blue, blue, blue, blue, blue}
	s := newScreen(64, 64, frames...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	hash, err := s.WaitForVisibleChange(ctx, image.Rect(0, 0, 64, 64), 1*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if hash == 0 {
		t.Error("expected non-zero settled hash")
	}
}

func TestWaitForVisibleChangeTimeout(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	// screen never changes
	s := newScreen(64, 64, red)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := s.WaitForVisibleChange(ctx, image.Rect(0, 0, 64, 64), 5*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// ── FindByTitle tests ─────────────────────────────────────────────────────────

func TestFindByTitleFound(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{
			{Title: "Firefox — Mozilla", W: 1280, H: 720},
			{Title: "Terminal"},
		}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	info, err := w.FindByTitle("firefox")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Firefox — Mozilla" {
		t.Errorf("wrong title: %q", info.Title)
	}
	if info.W != 1280 || info.H != 720 {
		t.Errorf("wrong geometry: %dx%d", info.W, info.H)
	}
}

func TestFindByTitleNotFound(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{
			{Title: "Terminal"},
		}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	_, err := w.FindByTitle("firefox")
	if err == nil {
		t.Error("expected error for no match")
	}
}

func TestFindByTitleCaseInsensitive(t *testing.T) {
	mgr := &mockManager{
		lists: [][]window.Info{{{Title: "KWrite — Untitled"}}},
	}
	w := perfuncted.WindowBundle{Manager: mgr}
	info, err := w.FindByTitle("KWRITE")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "KWrite — Untitled" {
		t.Errorf("unexpected title: %q", info.Title)
	}
}
