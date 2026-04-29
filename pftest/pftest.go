// Package pftest provides in-memory mock backends for perfuncted's
// Screenshotter, Inputter, Manager, and Clipboard interfaces.
package pftest

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"iter"
	"strings"
	"sync"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// ── Screenshotter ─────────────────────────────────────────────────────────────

type Screenshotter struct {
	Frames []image.Image
	Width  int
	Height int
	Err    error

	mu  sync.Mutex
	idx int
}

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

func (s *Screenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := s.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

func (s *Screenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

func (s *Screenshotter) Resolution() (int, int, error) {
	if len(s.Frames) > 0 {
		b := s.Frames[0].Bounds()
		return b.Dx(), b.Dy(), nil
	}
	return s.Width, s.Height, nil
}

func (s *Screenshotter) Reset() {
	s.mu.Lock()
	s.idx = 0
	s.mu.Unlock()
}

func (s *Screenshotter) Close() error { return nil }

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

func (m *Inputter) KeyDown(ctx context.Context, key string) error {
	m.record("down:" + key)
	return m.Err
}
func (m *Inputter) KeyUp(ctx context.Context, key string) error { m.record("up:" + key); return m.Err }
func (m *Inputter) KeyTap(ctx context.Context, key string) error {
	m.record("tap:" + key)
	return m.Err
}
func (m *Inputter) Type(ctx context.Context, s string) error {
	return m.TypeContext(ctx, s)
}
func (m *Inputter) TypeContext(ctx context.Context, s string) error {
	m.record("type:" + s)
	return m.Err
}
func (m *Inputter) MouseDown(ctx context.Context, b int) error { m.record("mousedown"); return m.Err }
func (m *Inputter) MouseUp(ctx context.Context, b int) error   { m.record("mouseup"); return m.Err }
func (m *Inputter) ScrollUp(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-up:%d", n))
	return m.Err
}
func (m *Inputter) ScrollDown(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-down:%d", n))
	return m.Err
}
func (m *Inputter) ScrollLeft(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-left:%d", n))
	return m.Err
}
func (m *Inputter) ScrollRight(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-right:%d", n))
	return m.Err
}
func (m *Inputter) MouseMove(ctx context.Context, x, y int) error {
	m.record(fmt.Sprintf("move:%d,%d", x, y))
	return m.Err
}
func (m *Inputter) MouseClick(ctx context.Context, x, y, b int) error {
	m.record(fmt.Sprintf("click:%d,%d", x, y))
	return m.Err
}
func (m *Inputter) PressCombo(ctx context.Context, c string) error {
	m.record("combo:" + c)
	return m.Err
}
func (m *Inputter) Close() error { return nil }

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

func (m *Inputter) Reset() {
	m.mu.Lock()
	m.Calls = m.Calls[:0]
	m.mu.Unlock()
}

// ── Manager ───────────────────────────────────────────────────────────────────

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
	var out []window.Info
	for win, err := range m.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

func (m *Manager) IterateWindows(ctx context.Context) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		if m.Err != nil {
			yield(window.Info{}, m.Err)
			return
		}
		m.mu.Lock()
		if len(m.Lists) == 0 {
			m.mu.Unlock()
			return
		}
		r := m.Lists[m.listIdx]
		if m.listIdx < len(m.Lists)-1 {
			m.listIdx++
		}
		m.mu.Unlock()

		for _, win := range r {
			if !yield(win, nil) {
				return
			}
		}
	}
}

func (m *Manager) Activate(ctx context.Context, title string) error {
	return m.ActivateContext(ctx, title)
}

func (m *Manager) ActivateContext(ctx context.Context, title string) error {
	if m.Err != nil {
		return m.Err
	}
	m.mu.Lock()
	m.Activated = append(m.Activated, title)
	m.mu.Unlock()
	return nil
}

func (m *Manager) ActiveTitle(ctx context.Context) (string, error) {
	return m.ActiveTitleContext(ctx)
}

func (m *Manager) ActiveTitleContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
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

func (m *Manager) FindByTitle(ctx context.Context, pattern string) (window.Info, error) {
	return window.FindByTitle(ctx, m, pattern)
}

func (m *Manager) Move(ctx context.Context, title string, x, y int) error   { return m.Err }
func (m *Manager) Resize(ctx context.Context, title string, w, h int) error { return m.Err }
func (m *Manager) CloseWindow(ctx context.Context, title string) error      { return m.Err }
func (m *Manager) Minimize(ctx context.Context, title string) error         { return m.Err }
func (m *Manager) Maximize(ctx context.Context, title string) error         { return m.Err }
func (m *Manager) Restore(ctx context.Context, title string) error          { return m.Err }
func (m *Manager) Close() error                                             { return nil }

func (m *Manager) Reset() {
	m.mu.Lock()
	m.listIdx = 0
	m.titleIdx = 0
	m.Activated = m.Activated[:0]
	m.mu.Unlock()
}

// ── Clipboard ─────────────────────────────────────────────────────────────────

type Clipboard struct {
	Text   string
	GetErr error
	SetErr error
}

func (c *Clipboard) Get(ctx context.Context) (string, error)    { return c.Text, c.GetErr }
func (c *Clipboard) Set(ctx context.Context, text string) error { c.Text = text; return c.SetErr }
func (c *Clipboard) Close() error                               { return nil }

// ── Assembly ──────────────────────────────────────────────────────────────────

func New(sc screen.Screenshotter, inp input.Inputter, mgr window.Manager, cb clipboard.Clipboard) *perfuncted.Perfuncted {
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
