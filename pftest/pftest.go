// Package pftest provides in-memory mock backends for perfuncted's
// Screenshotter, Inputter, Manager, and Clipboard interfaces.
//
// Each mock is a plain struct with exported fields — configure them before the
// test, run the code under test, assert on the recorded state afterward:
//
//	sc := &pftest.Screenshotter{
//	    Frames: []image.Image{pftest.SolidImage(64, 64, color.RGBA{255, 0, 0, 255})},
//	}
//	inp := &pftest.Inputter{}
//	pf := pftest.New(sc, inp, nil, nil)
//
//	pf.Input.PressCombo("ctrl+s")
//
//	if inp.Calls[0] != "down:ctrl" { ... }
//
// Pass nil for any backend you don't need — the corresponding bundle will have
// a nil inner interface and return an "not available" error if called, which is
// the same behaviour as perfuncted.New() when a backend fails to open.
package pftest

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/window"
)

// ── Screenshotter ─────────────────────────────────────────────────────────────

// Screenshotter is a deterministic, in-memory screen backend. It returns
// Frames in order; the last frame is repeated indefinitely once exhausted.
// If Frames is empty, Grab returns a zero-size black image.
//
// Width and Height are used by Resolution() and as the image size when a
// frame's bounds are zero. When Frames is non-empty, Resolution() returns
// the bounds of the first frame (ignoring Width/Height).
//
// Set Err to make all Grab calls return that error.
type Screenshotter struct {
	Frames []image.Image
	Width  int
	Height int
	Err    error

	mu  sync.Mutex
	idx int
}

// Grab returns the next frame. The rect argument is accepted but ignored —
// the mock always returns the full pre-configured image.
func (s *Screenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Frames) == 0 {
		w, h := s.Width, s.Height
		if w == 0 {
			w = 1
		}
		if h == 0 {
			h = 1
		}
		return image.NewRGBA(image.Rect(0, 0, w, h)), nil
	}
	f := s.Frames[s.idx]
	if s.idx < len(s.Frames)-1 {
		s.idx++
	}
	return f, nil
}

// Resolution returns the bounds of the first frame, or Width×Height when
// Frames is empty.
func (s *Screenshotter) Resolution() (int, int, error) {
	if len(s.Frames) > 0 {
		b := s.Frames[0].Bounds()
		return b.Dx(), b.Dy(), nil
	}
	return s.Width, s.Height, nil
}

// Reset rewinds the frame queue to the beginning.
func (s *Screenshotter) Reset() {
	s.mu.Lock()
	s.idx = 0
	s.mu.Unlock()
}

// Close is a no-op.
func (s *Screenshotter) Close() error { return nil }

// SolidImage returns a w×h *image.RGBA filled with c. Useful for building
// Screenshotter.Frames without loading files.
func SolidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// ── Inputter ──────────────────────────────────────────────────────────────────

// Inputter records every injected event as a string in Calls.
//
// Event strings follow the format "<verb>:<arg>" where verb is one of:
// "down", "up", "tap", "type", "move", "click", "mousedown", "mouseup",
// "scroll-up", "scroll-down", "scroll-left", "scroll-right".
// Mouse events append coordinates: "click:10,20" or "move:10,20".
// Scroll events append the click count: "scroll-up:3".
//
// Set Err to make every method return that error (after recording the call).
type Inputter struct {
	Calls []string
	Err   error

	mu sync.Mutex
}

func (m *Inputter) record(s string) {
	m.mu.Lock()
	m.Calls = append(m.Calls, s)
	m.mu.Unlock()
}

func (m *Inputter) KeyDown(key string) error { m.record("down:" + key); return m.Err }
func (m *Inputter) KeyUp(key string) error   { m.record("up:" + key); return m.Err }
func (m *Inputter) KeyTap(key string) error  { m.record("tap:" + key); return m.Err }
func (m *Inputter) Type(s string) error      { m.record("type:" + s); return m.Err }
func (m *Inputter) MouseDown(b int) error    { m.record("mousedown"); return m.Err }
func (m *Inputter) MouseUp(b int) error      { m.record("mouseup"); return m.Err }
func (m *Inputter) ScrollUp(n int) error     { m.record(fmt.Sprintf("scroll-up:%d", n)); return m.Err }
func (m *Inputter) ScrollDown(n int) error   { m.record(fmt.Sprintf("scroll-down:%d", n)); return m.Err }
func (m *Inputter) ScrollLeft(n int) error   { m.record(fmt.Sprintf("scroll-left:%d", n)); return m.Err }
func (m *Inputter) ScrollRight(n int) error {
	m.record(fmt.Sprintf("scroll-right:%d", n))
	return m.Err
}
func (m *Inputter) MouseMove(x, y int) error { m.record(fmt.Sprintf("move:%d,%d", x, y)); return m.Err }
func (m *Inputter) MouseClick(x, y, b int) error {
	m.record(fmt.Sprintf("click:%d,%d", x, y))
	return m.Err
}
func (m *Inputter) Close() error { return nil }

// Typed returns all "type:..." calls joined together, useful for asserting
// what text was typed overall.
func (m *Inputter) Typed() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for _, c := range m.Calls {
		if t, ok := strings.CutPrefix(c, "type:"); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// Reset clears the recorded call log.
func (m *Inputter) Reset() {
	m.mu.Lock()
	m.Calls = m.Calls[:0]
	m.mu.Unlock()
}

// ── Manager ───────────────────────────────────────────────────────────────────

// Manager provides scripted window lists and title sequences to drive
// WindowBundle logic without a live compositor.
//
// List() returns entries from Lists in order; the last entry is repeated.
// ActiveTitle() returns entries from Titles in order; the last is repeated.
// Activate() appends the resolved title to Activated.
//
// Set Err to make all methods return that error.
type Manager struct {
	Lists     [][]window.Info
	Titles    []string
	Err       error
	Activated []string

	mu       sync.Mutex
	listIdx  int
	titleIdx int
}

func (m *Manager) List(ctx context.Context) ([]window.Info, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Lists) == 0 {
		return nil, nil
	}
	r := m.Lists[m.listIdx]
	if m.listIdx < len(m.Lists)-1 {
		m.listIdx++
	}
	return r, nil
}

func (m *Manager) Activate(ctx context.Context, title string) error {
	if m.Err != nil {
		return m.Err
	}
	m.mu.Lock()
	m.Activated = append(m.Activated, title)
	m.mu.Unlock()
	return nil
}

func (m *Manager) ActiveTitle(ctx context.Context) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Titles) == 0 {
		return "", nil
	}
	t := m.Titles[m.titleIdx]
	if m.titleIdx < len(m.Titles)-1 {
		m.titleIdx++
	}
	return t, nil
}

func (m *Manager) Move(ctx context.Context, title string, x, y int) error   { return m.Err }
func (m *Manager) Resize(ctx context.Context, title string, w, h int) error { return m.Err }
func (m *Manager) CloseWindow(ctx context.Context, title string) error      { return m.Err }
func (m *Manager) Minimize(ctx context.Context, title string) error         { return m.Err }
func (m *Manager) Maximize(ctx context.Context, title string) error         { return m.Err }
func (m *Manager) Close() error                                             { return nil }

// Reset rewinds all queues and clears Activated.
func (m *Manager) Reset() {
	m.mu.Lock()
	m.listIdx = 0
	m.titleIdx = 0
	m.Activated = m.Activated[:0]
	m.mu.Unlock()
}

// ── Clipboard ─────────────────────────────────────────────────────────────────

// Clipboard is an in-memory clipboard backend. Get returns Text and GetErr;
// Set stores the provided text in Text and returns SetErr.
type Clipboard struct {
	Text   string
	GetErr error
	SetErr error
}

func (c *Clipboard) Get() (string, error)  { return c.Text, c.GetErr }
func (c *Clipboard) Set(text string) error { c.Text = text; return c.SetErr }
func (c *Clipboard) Close() error          { return nil }

// ── Assembly ──────────────────────────────────────────────────────────────────

// New assembles a *perfuncted.Perfuncted wired with the supplied mock backends.
// Pass nil for any backend you don't need; the corresponding bundle will behave
// as it does when perfuncted.New() fails to open that backend.
func New(sc *Screenshotter, inp *Inputter, mgr *Manager, cb *Clipboard) *perfuncted.Perfuncted {
	pf := &perfuncted.Perfuncted{}
	if sc != nil {
		pf.Screen = perfuncted.ScreenBundle{Screenshotter: sc}
	}
	if inp != nil {
		pf.Input = perfuncted.InputBundle{Inputter: inp}
	}
	if mgr != nil {
		pf.Window = perfuncted.WindowBundle{Manager: mgr}
	}
	if cb != nil {
		pf.Clipboard = perfuncted.ClipboardBundle{Clipboard: cb}
	}
	return pf
}
