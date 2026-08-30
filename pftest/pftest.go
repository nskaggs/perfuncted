// Package pftest provides in-memory mock backends for perfuncted's
// Screenshotter, Inputter, Manager, and Clipboard interfaces.
package pftest

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"iter"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// ── Screenshotter ─────────────────────────────────────────────────────────────

// Screenshotter is an in-memory screen backend that returns configured frames.
type Screenshotter struct {
	// Frames are returned in order and the last frame is reused thereafter.
	Frames []image.Image
	// Width is the fallback image width when Frames is empty.
	Width int
	// Height is the fallback image height when Frames is empty.
	Height int
	// Err is returned by capture operations when non-nil.
	Err error
	// ZeroOrigin rebases returned images to an origin of (0, 0).
	ZeroOrigin bool

	mu  sync.Mutex
	idx int
}

// Grab returns the next configured frame, optionally cropped to rect.
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
	if !s.ZeroOrigin {
		return f, nil
	}
	target := rect
	if target.Empty() {
		target = f.Bounds()
	}
	target = target.Intersect(f.Bounds())
	if target.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0)), nil
	}
	out := image.NewRGBA(image.Rect(0, 0, target.Dx(), target.Dy()))
	draw.Draw(out, out.Bounds(), f, target.Min, draw.Src)
	return out, nil
}

// GrabFullHash hashes the next full-screen frame.
func (s *Screenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := s.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// GrabRegionHash hashes the next frame cropped to rect.
func (s *Screenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// Resolution returns the configured or first-frame dimensions.
func (s *Screenshotter) Resolution() (int, int, error) {
	if len(s.Frames) > 0 {
		b := s.Frames[0].Bounds()
		return b.Dx(), b.Dy(), nil
	}
	return s.Width, s.Height, nil
}

// Reset starts frame playback from the first frame.
func (s *Screenshotter) Reset() {
	s.mu.Lock()
	s.idx = 0
	s.mu.Unlock()
}

// Close implements screen.Screenshotter and releases no resources.
func (s *Screenshotter) Close() error { return nil }

// SolidImage returns a solid-color RGBA image of the requested size.
func SolidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

// ── Inputter ──────────────────────────────────────────────────────────────────

// Inputter records input operations in memory for tests.
type Inputter struct {
	// Calls contains the recorded operation strings.
	Calls []string
	// Err is returned by input operations when non-nil.
	Err error

	mu sync.Mutex
}

func (m *Inputter) record(s string) {
	m.mu.Lock()
	m.Calls = append(m.Calls, s)
	m.mu.Unlock()
}

// KeyDown records a key press.
func (m *Inputter) KeyDown(ctx context.Context, key string) error {
	m.record("down:" + key)
	return m.Err
}

// KeyUp records a key release.
func (m *Inputter) KeyUp(ctx context.Context, key string) error { m.record("up:" + key); return m.Err }

// Type records typed text.
func (m *Inputter) Type(ctx context.Context, s string) error {
	return m.typeContext(ctx, s)
}
func (m *Inputter) typeContext(_ context.Context, s string) error {
	m.record("type:" + s)
	return m.Err
}

// TypeLiteral records typed text without key-syntax interpretation.
func (m *Inputter) TypeLiteral(_ context.Context, s string) error {
	m.record("type-literal:" + s)
	return m.Err
}

// MouseDown records a mouse-button press.
func (m *Inputter) MouseDown(ctx context.Context, b int) error { m.record("mousedown"); return m.Err }

// MouseUp records a mouse-button release.
func (m *Inputter) MouseUp(ctx context.Context, b int) error { m.record("mouseup"); return m.Err }

// ScrollUp records upward scrolling.
func (m *Inputter) ScrollUp(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-up:%d", n))
	return m.Err
}

// ScrollDown records downward scrolling.
func (m *Inputter) ScrollDown(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-down:%d", n))
	return m.Err
}

// ScrollLeft records leftward scrolling.
func (m *Inputter) ScrollLeft(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-left:%d", n))
	return m.Err
}

// ScrollRight records rightward scrolling.
func (m *Inputter) ScrollRight(ctx context.Context, n int) error {
	m.record(fmt.Sprintf("scroll-right:%d", n))
	return m.Err
}

// MouseMove records pointer movement.
func (m *Inputter) MouseMove(ctx context.Context, x, y int) error {
	m.record("move:" + strconv.Itoa(x) + "," + strconv.Itoa(y))
	return m.Err
}

// MouseClick records a button click.
func (m *Inputter) MouseClick(ctx context.Context, x, y, b int) error {
	m.record("click:" + strconv.Itoa(x) + "," + strconv.Itoa(y))
	return m.Err
}

// PointerLocation returns the zero test position and configured error.
func (m *Inputter) PointerLocation(ctx context.Context) (int, int, error) {
	return 0, 0, m.Err
}

// Sync returns the configured error.
func (m *Inputter) Sync(ctx context.Context) error { return m.Err }

// Close implements input.Inputter and releases no resources.
func (m *Inputter) Close() error { return nil }

// Typed returns the concatenated text recorded by Type.
func (m *Inputter) Typed() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for _, c := range m.Calls {
		if t, ok := strings.CutPrefix(c, "type:"); ok {
			b.WriteString(t)
			continue
		}
		if t, ok := strings.CutPrefix(c, "type-literal:"); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// Reset clears recorded calls.
func (m *Inputter) Reset() {
	m.mu.Lock()
	m.Calls = m.Calls[:0]
	m.mu.Unlock()
}

// ── Manager ───────────────────────────────────────────────────────────────────

// Manager is an in-memory window backend for tests.
type Manager struct {
	// Lists contains successive window-list results.
	Lists [][]window.Info
	// Titles contains successive active-window titles.
	Titles []string
	// Err is returned by backend operations when non-nil.
	Err error
	// Activated records IDs passed to ActivateByID.
	Activated []string

	mu       sync.Mutex
	listIdx  int
	titleIdx int
}

// List returns the next configured window list.
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

// IterateWindows iterates over the next configured window list.
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

func (m *Manager) activateContext(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Err != nil {
		return m.Err
	}
	m.mu.Lock()
	m.Activated = append(m.Activated, id)
	m.mu.Unlock()
	return nil
}

// ActiveTitle returns the next configured active-window title.
func (m *Manager) ActiveTitle(ctx context.Context) (string, error) {
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

// FindByTitle finds a configured window by title substring.
func (m *Manager) FindByTitle(ctx context.Context, pattern string) (window.Info, error) {
	return window.FindByTitle(ctx, m, pattern)
}

// Close implements window.Manager and releases no resources.
func (m *Manager) Close() error { return nil }

// ActivateByID records an activation request.
func (m *Manager) ActivateByID(ctx context.Context, id string) error {
	return m.activateContext(ctx, id)
}

// MoveByID returns the configured backend error.
func (m *Manager) MoveByID(ctx context.Context, id string, x, y int) error {
	return m.Err
}

// ResizeByID returns the configured backend error.
func (m *Manager) ResizeByID(ctx context.Context, id string, w, h int) error {
	return m.Err
}

// CloseWindowByID returns the configured backend error.
func (m *Manager) CloseWindowByID(ctx context.Context, id string) error {
	return m.Err
}

// MinimizeByID returns the configured backend error.
func (m *Manager) MinimizeByID(ctx context.Context, id string) error { return m.Err }

// MaximizeByID returns the configured backend error.
func (m *Manager) MaximizeByID(ctx context.Context, id string) error { return m.Err }

// FullscreenByID returns the configured backend error.
func (m *Manager) FullscreenByID(ctx context.Context, id string) error {
	return m.Err
}

// UnfullscreenByID returns the configured backend error.
func (m *Manager) UnfullscreenByID(ctx context.Context, id string) error {
	return m.Err
}

// RestoreByID returns the configured backend error.
func (m *Manager) RestoreByID(ctx context.Context, id string) error {
	return m.Err
}

// InfoByID returns the configured window with id.
func (m *Manager) InfoByID(
	ctx context.Context,
	id string,
) (window.Info, error) {
	windows, err := m.List(ctx)
	if err != nil {
		return window.Info{}, err
	}
	for _, info := range windows {
		if info.StableID() == id {
			return info, nil
		}
	}
	return window.Info{}, window.ErrWindowNotFound
}

// Reset restarts configured result playback and clears activations.
func (m *Manager) Reset() {
	m.mu.Lock()
	m.listIdx = 0
	m.titleIdx = 0
	m.Activated = m.Activated[:0]
	m.mu.Unlock()
}

// ── Clipboard ─────────────────────────────────────────────────────────────────

// Clipboard is an in-memory clipboard backend for tests.
type Clipboard struct {
	// Text is returned by Get and updated by Set.
	Text string
	// GetErr is returned by Get when non-nil.
	GetErr error
	// SetErr is returned by Set when non-nil.
	SetErr error
}

// Get returns Text and GetErr.
func (c *Clipboard) Get(ctx context.Context) (string, error) { return c.Text, c.GetErr }

// Set updates Text and returns SetErr.
func (c *Clipboard) Set(ctx context.Context, text string) error { c.Text = text; return c.SetErr }

// Close implements clipboard.Clipboard and releases no resources.
func (c *Clipboard) Close() error { return nil }

// ── Assembly ──────────────────────────────────────────────────────────────────

// New assembles a test Session from mock backends.
func New(sc screen.Screenshotter, inp input.Inputter, mgr window.Manager, cb clipboard.Clipboard) *perfuncted.Session {
	return perfuncted.NewSessionForTesting(sc, inp, mgr, nil, cb)
}

// NewWithOutputs assembles a Session that also provides output discovery.
func NewWithOutputs(
	sc screen.Screenshotter,
	inp input.Inputter,
	mgr window.Manager,
	out output.Lister,
	cb clipboard.Clipboard,
) *perfuncted.Session {
	return perfuncted.NewSessionForTesting(sc, inp, mgr, out, cb)
}

// CaptureOnFailure registers an opt-in test cleanup that writes a diagnostic
// bundle when t fails. If directory is empty, artifacts are written to a
// temporary directory. The bundle may contain screenshots, window titles,
// process IDs, and desktop topology; treat it as sensitive.
func CaptureOnFailure(t testing.TB, session *perfuncted.Session, directory string) {
	if t == nil || session == nil {
		return
	}
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		path, err := session.CaptureFailureBundle(ctx, perfuncted.FailureBundleOptions{
			Directory: directory,
			Operation: "test " + t.Name(),
		})
		if err != nil {
			t.Logf("failure bundle written to %s with collection errors: %v", path, err)
			return
		}
		t.Logf("failure bundle written to %s", path)
	})
}
