// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
//
// Three top-level bundles cover all automation needs:
//   - [PF.Screen] — capture regions, hash pixels, locate images, wait for changes
//   - [PF.Input] — type text, tap keys, click and drag, scroll
//   - [PF.Window] — list, activate, resize, and wait for windows
//
// Quick start:
//
//	pf, err := perfuncted.New(perfuncted.Options{})
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer pf.Close()
//
//	pf.Window.Activate("Firefox")
//	pf.Input.Type("hello world")
//	pf.Input.KeyTap("ctrl+s")
package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/wl"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// Options controls backend selection.
type Options struct {
	// MaxX and MaxY define the absolute coordinate space for uinput's touch-pad
	// device. Set these to your primary monitor's resolution. These values are
	// only used by the uinput backend (which creates a kernel-level virtual
	// touchpad requiring explicit axis ranges). The Wayland virtual-pointer
	// backend (wlroots) auto-detects output dimensions from the compositor and
	// ignores MaxX/MaxY. Defaults: 1920×1080.
	MaxX, MaxY int32

	// Nested, when true, causes New() to auto-detect a nested perfuncted sway
	// session in /tmp/perfuncted-xdg-* while opening backends against that
	// session instead of the host desktop.
	Nested bool

	// XDGRuntimeDir, WaylandDisplay, and DBusSessionAddress allow callers to
	// specify the session environment directly instead of relying on the
	// process environment. This is the preferred way to connect to a specific
	// session — use it instead of calling os.Setenv manually.
	XDGRuntimeDir      string
	WaylandDisplay     string
	DBusSessionAddress string
}

type sessionEnv struct {
	xdgRuntimeDir      string
	waylandDisplay     string
	dbusSessionAddress string
}

// NestedEnv scans /tmp/perfuncted-xdg-* directories created by `just nested` and
// returns the environment variables needed to connect to a nested sway session.
// Returns the XDG_RUNTIME_DIR, WAYLAND_DISPLAY, DBUS_SESSION_BUS_ADDRESS, and
// an error if no nested session is found.
func NestedEnv() (xdgRuntimeDir, waylandDisplay, dbusAddr string, err error) {
	matches, err := filepath.Glob("/tmp/perfuncted-xdg-*")
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob nested sessions: %w", err)
	}
	if len(matches) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found in /tmp/perfuncted-xdg-*")
	}
	if len(matches) > 1 {
		return "", "", "", fmt.Errorf("perfuncted: multiple nested sessions found (%d), specify env vars manually", len(matches))
	}

	xdgDir := matches[0]

	// Find the wayland-* socket (not the .lock file)
	sockets, err := filepath.Glob(filepath.Join(xdgDir, "wayland-*"))
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob wayland sockets: %w", err)
	}
	var wlSocket string
	for _, sock := range sockets {
		if !strings.HasSuffix(sock, ".lock") {
			wlSocket = filepath.Base(sock)
			break
		}
	}
	if wlSocket == "" {
		return "", "", "", fmt.Errorf("perfuncted: no wayland socket in %s", xdgDir)
	}

	return xdgDir, wlSocket, fmt.Sprintf("unix:path=%s/bus", xdgDir), nil
}

// DetectSession reports which session the current environment targets.
// Returns "nested", "host", or "unknown", with a details map for each.
func DetectSession() (kind string, details map[string]string) {
	details = make(map[string]string)

	xdg := os.Getenv("XDG_RUNTIME_DIR")
	wd := os.Getenv("WAYLAND_DISPLAY")

	// Check if this is a perfuncted nested session
	if strings.HasPrefix(xdg, "/tmp/perfuncted-xdg-") {
		details["dir"] = xdg
		details["wayland_display"] = wd
		details["dbus_address"] = os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		return "nested", details
	}

	// Check if a nested session exists but we're not connected to it
	matches, err := filepath.Glob("/tmp/perfuncted-xdg-*")
	if err == nil && len(matches) > 0 {
		details["available_session"] = matches[0]
		details["current_xdg"] = xdg
		details["current_wayland"] = wd
		return "host", details
	}

	details["current_xdg"] = xdg
	details["current_wayland"] = wd
	return "host", details
}

// ScreenBundle wraps a screen.Screenshotter with additional find utilities.
type ScreenBundle struct {
	screen.Screenshotter
}

func (s ScreenBundle) checkAvailable() error {
	if s.Screenshotter == nil {
		return fmt.Errorf("screen: not available")
	}
	return nil
}

// GrabHash captures a region and returns its pixel hash.
func (s ScreenBundle) GrabHash(rect image.Rectangle) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.GrabHash(context.Background(), s.Screenshotter, rect, nil)
}

// WaitForChange polls rect every poll interval until its hash differs from initial.
// It pairs with WaitForNoChange: use WaitForChange to detect when a transition begins,
// then WaitForNoChange to detect when it ends.
func (s ScreenBundle) WaitForChange(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

// WaitForNoChange polls rect until its pixel hash is unchanged for stable consecutive
// samples. Use after WaitForChange (or after triggering an action) to detect when the
// UI has finished settling — e.g. a page has loaded, a dialog closed.
//
// stable must be ≥ 1; a value of 5 at 200ms poll requires one second of visual stability.
func (s ScreenBundle) WaitForNoChange(ctx context.Context, rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

// WaitForSettle captures the initial hash of rect, calls action, waits for the region to
// change (WaitForChange), then waits for it to stop changing (WaitForNoChange). This is the
// canonical "do something and wait for the UI to finish responding" primitive.
//
// stable controls how many consecutive identical samples count as settled.
// A value of 5 at 200ms poll means one second of visual stability.
func (s ScreenBundle) WaitForSettle(ctx context.Context, rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := find.GrabHash(context.Background(), s.Screenshotter, rect, nil)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

// WaitFor polls rect until its hash equals want.
func (s ScreenBundle) WaitFor(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

// ScanFor polls multiple regions until one matches its expected hash.
func (s ScreenBundle) ScanFor(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

// LocateExact searches for reference image within searchArea using exact pixel matching.
func (s ScreenBundle) LocateExact(searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.LocateExact(context.Background(), s.Screenshotter, searchArea, reference)
}

// Resolution returns the screen width and height in pixels.
func (s ScreenBundle) Resolution() (int, int, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.Resolution(s.Screenshotter)
}

// GrabFull captures the entire output at its native resolution.
func (s ScreenBundle) GrabFull() (image.Image, error) {
	w, h, err := s.Resolution()
	if err != nil {
		return nil, err
	}
	return s.Screenshotter.Grab(context.Background(), image.Rect(0, 0, w, h))
}

// GrabFullHash captures the entire output and returns its pixel hash.
func (s ScreenBundle) GrabFullHash() (uint32, error) {
	w, h, err := s.Resolution()
	if err != nil {
		return 0, err
	}
	return s.GrabHash(image.Rect(0, 0, w, h))
}

// WaitForStable polls rect until its pixel hash is unchanged for stableN
// consecutive samples, then returns the settled hash. Unlike WaitForVisibleChange,
// it does not require an initial change to have occurred first — use it when the
// UI is already mid-transition or to confirm it has finished settling from the
// current state (e.g. after openConsole or Activate).
//
// stableN must be ≥ 1; a value of 5 at 200ms poll requires one second of stability.
func (s ScreenBundle) WaitForStable(ctx context.Context, rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stableN, poll, nil)
}

// WaitForVisibleChange grabs the initial state of rect, waits for it to change,
// then waits for it to stabilise. Use immediately after triggering an action
// (navigation, button press, dialog open) to detect when the UI has settled.
//
// stable is the number of consecutive identical samples required to consider
// the screen settled. It defaults to 3 when not provided.
//
// It is equivalent to: grab hash → WaitForChange → WaitForNoChange(stable samples).
func (s ScreenBundle) WaitForVisibleChange(ctx context.Context, rect image.Rectangle, poll time.Duration, stable ...int) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	stableN := 3
	if len(stable) > 0 && stable[0] > 0 {
		stableN = stable[0]
	}
	initial, err := find.GrabHash(context.Background(), s.Screenshotter, rect, nil)
	if err != nil {
		return 0, err
	}
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stableN, poll, nil)
}

// WaitForFn polls rect every poll interval until fn returns true for the
// grabbed image, or ctx expires. fn receives the raw grabbed image and may
// inspect it with any predicate (colour presence, brightness, histogram, etc.).
func (s ScreenBundle) WaitForFn(ctx context.Context, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

// WaitWithTolerance waits for targetHash to appear within radius pixels of expectedRect.
func (s ScreenBundle) WaitWithTolerance(ctx context.Context, expectedRect image.Rectangle, targetHash uint32, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, image.Rectangle{}, err
	}
	return find.WaitWithTolerance(ctx, s.Screenshotter, expectedRect, targetHash, radius, poll, nil)
}

// WaitForLocate polls searchArea until reference image is found via exact pixel
// matching, or ctx expires.
func (s ScreenBundle) WaitForLocate(ctx context.Context, searchArea image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.WaitForLocate(ctx, s.Screenshotter, searchArea, reference, poll)
}

// FindColor scans rect for the first pixel matching target colour within
// tolerance per channel. Returns the absolute point of the match.
func (s ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(context.Background(), s.Screenshotter, rect, target, tolerance)
}

// GetPixel returns the colour of the pixel at (x,y) on the screen.
func (s ScreenBundle) GetPixel(x, y int) (color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	return find.FirstPixel(context.Background(), s.Screenshotter, image.Rect(x, y, x+1, y+1))
}

// GetMultiplePixels returns the colours at the requested absolute screen points.
// It grabs the minimal bounding box covering the points to minimise IPCs.
func (s ScreenBundle) GetMultiplePixels(points []image.Point) ([]color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return nil, nil
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := points[0].X, points[0].Y
	for _, p := range points {
		if p.X < minX {
			minX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	bbox := image.Rect(minX, minY, maxX+1, maxY+1)
	img, err := s.Screenshotter.Grab(context.Background(), bbox)
	if err != nil {
		// Fallback to individual reads.
		out := make([]color.RGBA, len(points))
		for i, p := range points {
			c, err := find.FirstPixel(context.Background(), s.Screenshotter, image.Rect(p.X, p.Y, p.X+1, p.Y+1))
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	b := img.Bounds()
	out := make([]color.RGBA, len(points))
	for i, p := range points {
		x := p.X - bbox.Min.X + b.Min.X
		y := p.Y - bbox.Min.Y + b.Min.Y
		out[i] = color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	}
	return out, nil
}

// CaptureRegion captures rect and writes it as a PNG to path.
func (s ScreenBundle) CaptureRegion(rect image.Rectangle, path string) error {
	if err := s.checkAvailable(); err != nil {
		return err
	}
	img, err := s.Screenshotter.Grab(context.Background(), rect)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("screen: create file: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("screen: encode png: %w", err)
	}
	return nil
}

// PixelToScreen returns the raw grabbed image for rect. It is a thin wrapper
// over the Screenshotter's Grab method but provided for API symmetry.
func (s ScreenBundle) PixelToScreen(rect image.Rectangle) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return s.Screenshotter.Grab(context.Background(), rect)
}

// WindowBundle wraps a window.Manager with additional find utilities.
type WindowBundle struct {
	window.Manager
}

// Backwards-compatible wrappers: preserve old zero-context calls by delegating
// to the new context-aware Manager using context.Background(). These are
// convenience shims for callers (CLI/tests) that haven't migrated yet.
func (w WindowBundle) List() ([]window.Info, error) {
	if w.Manager == nil {
		return nil, fmt.Errorf("window: not available")
	}
	return w.Manager.List(context.Background())
}

func (w WindowBundle) ActiveTitle() (string, error) {
	if w.Manager == nil {
		return "", fmt.Errorf("window: not available")
	}
	return w.Manager.ActiveTitle(context.Background())
}

func (w WindowBundle) CloseWindow(title string) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return w.Manager.CloseWindow(context.Background(), title)
}

func (w WindowBundle) Move(title string, x, y int) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return w.Manager.Move(context.Background(), title, x, y)
}

func (w WindowBundle) Resize(title string, width, height int) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return w.Manager.Resize(context.Background(), title, width, height)
}

func (w WindowBundle) Minimize(title string) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return w.Manager.Minimize(context.Background(), title)
}

func (w WindowBundle) Maximize(title string) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return w.Manager.Maximize(context.Background(), title)
}

// Activate focuses the first window whose title contains pattern (case-insensitive).
// Note: This operates on a "first match wins" basis.
func (w WindowBundle) Activate(pattern string) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	info, err := window.FindByTitle(context.Background(), w.Manager, pattern)
	if err != nil {
		return fmt.Errorf("window: %w", err)
	}
	return w.Manager.Activate(context.Background(), info.Title)
}

// FindByTitle returns the first window whose title contains pattern
// (case-insensitive). Use this to inspect window geometry or title before
// activating; Activate discards the match result.
func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	if w.Manager == nil {
		return window.Info{}, fmt.Errorf("window: not available")
	}
	info, err := window.FindByTitle(context.Background(), w.Manager, pattern)
	if err != nil {
		return window.Info{}, fmt.Errorf("window: %w", err)
	}
	return info, nil
}

// IsVisible reports whether any window whose title contains pattern
// (case-insensitive) is currently open.
func (w WindowBundle) IsVisible(pattern string) bool {
	_, err := w.FindByTitle(pattern)
	return err == nil
}

// Restore attempts to bring the window matching title back to a normal state.
// This is best-effort: Activate is used to un-minimize; un-maximizing is
// backend-specific and may not be supported uniformly.
func (w WindowBundle) Restore(title string) error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	// Prefer backend-specific Restore if available.
	if r, ok := w.Manager.(interface{ Restore(string) error }); ok {
		return r.Restore(title)
	}
	// Fallback: Activate to un-minimize/raise as a best-effort restore.
	if err := w.Activate(title); err != nil {
		return fmt.Errorf("window: restore not supported or failed: %w", err)
	}
	return nil
}

// GetGeometry returns the window geometry (x,y,w,h) for the first matching title.
func (w WindowBundle) GetGeometry(title string) (image.Rectangle, error) {
	info, err := w.FindByTitle(title)
	if err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H), nil
}

// GetProcess returns the PID of the process that owns the first window matching title.
func (w WindowBundle) GetProcess(title string) (int, error) {
	info, err := w.FindByTitle(title)
	if err != nil {
		return 0, err
	}
	return int(info.PID), nil
}

// WaitFor polls the window list until a window whose title contains pattern
// (case-insensitive) appears, or ctx is cancelled. List() errors are propagated
// rather than silently swallowed.
func (w WindowBundle) WaitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	lower := strings.ToLower(pattern)
	for {
		wins, err := w.Manager.List(ctx)
		if err != nil {
			return window.Info{}, fmt.Errorf("window: list: %w", err)
		}
		for _, win := range wins {
			if strings.Contains(strings.ToLower(win.Title), lower) {
				return win, nil
			}
		}
		select {
		case <-ctx.Done():
			return window.Info{}, fmt.Errorf("window %q did not appear: %w", pattern, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// WaitForClose polls the window list until no window whose title contains
// pattern (case-insensitive) is present, or ctx is cancelled.
func (w WindowBundle) WaitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	lower := strings.ToLower(pattern)
	for {
		wins, err := w.Manager.List(ctx)
		if err != nil {
			return fmt.Errorf("window: list: %w", err)
		}
		found := false
		for _, win := range wins {
			if strings.Contains(strings.ToLower(win.Title), lower) {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("window %q did not close: %w", pattern, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// WaitForTitleChange polls ActiveTitle until it differs from current, or ctx
// is cancelled. Returns the new active title.
func (w WindowBundle) WaitForTitleChange(ctx context.Context, poll time.Duration) (string, error) {
	current, err := w.Manager.ActiveTitle(context.Background())
	if err != nil {
		return "", fmt.Errorf("window: active title: %w", err)
	}
	for {
		title, err := w.Manager.ActiveTitle(context.Background())
		if err != nil {
			return "", fmt.Errorf("window: active title: %w", err)
		}
		if title != current {
			return title, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("window title did not change from %q: %w", current, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// InputBundle wraps an input.Inputter with higher-level workflow methods.
type InputBundle struct {
	input.Inputter
}

func (i InputBundle) checkAvailable() error {
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	return nil
}

// DoubleClick moves to (x, y) and performs two quick left clicks with a
// short inter-click delay so the target application registers a double-click.
func (i InputBundle) DoubleClick(x, y int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.MouseClick(context.Background(), x, y, 1); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return i.MouseClick(context.Background(), x, y, 1)
}

// DragAndDrop moves to (x1, y1), presses left button, moves to (x2, y2), and releases.
func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.MouseMove(context.Background(), x1, y1); err != nil {
		return err
	}
	if err := i.MouseDown(context.Background(), 1); err != nil {
		return err
	}
	if err := i.MouseMove(context.Background(), x2, y2); err != nil {
		i.MouseUp(context.Background(), 1) // best effort release
		return err
	}
	return i.MouseUp(context.Background(), 1)
}

// ClickCenter moves to the center of rect and performs a left click.
func (i InputBundle) ClickCenter(rect image.Rectangle) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	cx := rect.Min.X + rect.Dx()/2
	cy := rect.Min.Y + rect.Dy()/2
	return i.MouseClick(context.Background(), cx, cy, 1)
}

// PressCombo sends a key combination like "ctrl+s" or "alt+f4".
// Modifiers are held down in order, the final key is tapped, then
// modifiers are released in reverse order.
func (i InputBundle) PressCombo(combo string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if combo == "" {
		return fmt.Errorf("input: PressCombo: combo must not be empty")
	}
	parts := strings.Split(strings.ToLower(combo), "+")
	modifiers := parts[:len(parts)-1]
	final := parts[len(parts)-1]
	for _, m := range modifiers {
		if err := i.KeyDown(context.Background(), m); err != nil {
			return err
		}
	}
	if err := i.KeyTap(context.Background(), final); err != nil {
		for _, m := range modifiers {
			i.KeyUp(context.Background(), m) //nolint:errcheck
		}
		return err
	}
	for ix := len(modifiers) - 1; ix >= 0; ix-- {
		if err := i.KeyUp(context.Background(), modifiers[ix]); err != nil {
			return err
		}
	}
	return nil
}

// ModifierDown presses and holds a modifier key (e.g. "ctrl", "alt", "shift").
func (i InputBundle) ModifierDown(key string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.KeyDown(context.Background(), key)
}

// ModifierUp releases a previously held modifier key.
func (i InputBundle) ModifierUp(key string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.KeyUp(context.Background(), key)
}

// TypeWithDelay types the provided string, waiting delay between each rune.
func (i InputBundle) TypeWithDelay(s string, delay time.Duration) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	for _, r := range s {
		if err := i.Type(context.Background(), string(r)); err != nil {
			return err
		}
		time.Sleep(delay)
	}
	return nil
}

// Raw injects a raw scancode if the underlying Inputter supports it.
func (i InputBundle) Raw(scancode int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if r, ok := i.Inputter.(interface{ Raw(int) error }); ok {
		return r.Raw(scancode)
	}
	return fmt.Errorf("input: raw scancode not supported by backend")
}

// ClipboardBundle wraps clipboard.Clipboard with a safe Get that won't hang.
type ClipboardBundle struct {
	clipboard.Clipboard
}

// PasteWithInput sets the clipboard text and issues a paste keystroke (Ctrl+V)
// via the supplied Inputter. Use this in tests by passing pftest.Clipboard and
// pftest.Inputter to avoid spawning external clipboard helpers.
func (c ClipboardBundle) PasteWithInput(inp input.Inputter, text string) error {
	if c.Clipboard == nil {
		return fmt.Errorf("clipboard: not available")
	}
	if inp == nil {
		return fmt.Errorf("input: not available")
	}
	if err := c.Set(text); err != nil {
		return err
	}
	// small delay to ensure clipboard contents are available to target apps
	time.Sleep(75 * time.Millisecond)
	if err := inp.KeyDown(context.Background(), "ctrl"); err != nil {
		return err
	}
	if err := inp.KeyTap(context.Background(), "v"); err != nil {
		_ = inp.KeyUp(context.Background(), "ctrl")
		return err
	}
	return inp.KeyUp(context.Background(), "ctrl")
}

// Get reads the clipboard. The default clipboard backends enforce their own
// command timeout so callers do not block indefinitely on hung tools.
func (c ClipboardBundle) Get() (string, error) {
	if c.Clipboard == nil {
		return "", fmt.Errorf("clipboard: not available")
	}
	return c.Clipboard.Get()
}

// Paste sets the clipboard text and issues a paste keystroke (Ctrl+V) using the
// Perfuncted.Input bundle. This makes paste testable by injecting mock
// Clipboard and Input backends (use pftest.New in tests).
func (pf *Perfuncted) Paste(text string) error {
	if pf == nil {
		return fmt.Errorf("perfuncted: nil")
	}
	if pf.Clipboard.Clipboard == nil {
		return fmt.Errorf("clipboard: not available")
	}
	if err := pf.Clipboard.Set(text); err != nil {
		return err
	}
	// small delay to ensure clipboard contents are available to target apps
	time.Sleep(75 * time.Millisecond)
	if pf.Input.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	return pf.Input.PressCombo("ctrl+v")
}

// Perfuncted bundles auto-detected screen, input, window, and clipboard backends.
type Perfuncted struct {
	Screen    ScreenBundle
	Input     InputBundle
	Window    WindowBundle
	Clipboard ClipboardBundle
}

func resolveSessionEnv(opts Options) (sessionEnv, error) {
	env := sessionEnv{
		xdgRuntimeDir:      opts.XDGRuntimeDir,
		waylandDisplay:     opts.WaylandDisplay,
		dbusSessionAddress: opts.DBusSessionAddress,
	}
	if opts.Nested {
		xdg, wd, dbus, err := NestedEnv()
		if err != nil {
			return sessionEnv{}, fmt.Errorf("perfuncted: nested session: %w", err)
		}
		if env.xdgRuntimeDir == "" {
			env.xdgRuntimeDir = xdg
		}
		if env.waylandDisplay == "" {
			env.waylandDisplay = wd
		}
		if env.dbusSessionAddress == "" {
			env.dbusSessionAddress = dbus
		}
	}
	return env, nil
}

// applySessionEnv sets package-level overrides (instead of mutating process
// environment) so session targeting is thread-safe. It returns a restore
// function that clears the overrides and restores previous socket selection.
func applySessionEnv(env sessionEnv) func() {
	// capture previous socket path to restore behavior afterwards
	prevSock := wl.SocketPath()
	// set socket override based on provided env
	if env.waylandDisplay != "" {
		if filepath.IsAbs(env.waylandDisplay) {
			wl.SetSocketPathOverride(env.waylandDisplay)
		} else if env.xdgRuntimeDir != "" {
			wl.SetSocketPathOverride(filepath.Join(env.xdgRuntimeDir, env.waylandDisplay))
		} else {
			// relative name with no xdg provided — still set override to name
			wl.SetSocketPathOverride(env.waylandDisplay)
		}
	}
	// DBus override
	dbusutil.SetSessionAddressOverride(env.dbusSessionAddress)
	// DISPLAY override (empty here — callers may set via package helpers)
	screen.SetDisplayOverride("")
	return func() {
		// restore previous state
		wl.SetSocketPathOverride(prevSock)
		dbusutil.SetSessionAddressOverride("")
		screen.SetDisplayOverride("")
	}
}

// New opens all backends using auto-detection. Each backend is attempted
// independently; an error from one does not prevent the others from opening.
// Returns an error only when no backend could be opened at all.
//
// When opts.Nested or explicit session values are provided, New() temporarily
// targets that session while the backends are opened, then restores the
// process environment before returning.
func New(opts Options) (*Perfuncted, error) {
	env, err := resolveSessionEnv(opts)
	if err != nil {
		return nil, err
	}
	// Instead of mutating process environment, set package-level overrides
	// so backends can connect to the desired session without global state races.
	// Prepare restore function to clear overrides when New returns.
	// internal/wl.SetSocketPathOverride exists; import via package wl
	wl.SetSocketPathOverride("")
	// set to requested socket path
	if env.waylandDisplay != "" {
		// compute absolute socket path from XDG_RUNTIME_DIR + WAYLAND_DISPLAY
		if filepath.IsAbs(env.waylandDisplay) {
			wl.SetSocketPathOverride(env.waylandDisplay)
		} else if env.xdgRuntimeDir != "" {
			wl.SetSocketPathOverride(filepath.Join(env.xdgRuntimeDir, env.waylandDisplay))
		}
	}
	// DBus override
	dbusutil.SetSessionAddressOverride(env.dbusSessionAddress)
	// DISPLAY override for screen/input/window packages
	screen.SetDisplayOverride("")
	defer func() {
		// clear overrides
		wl.SetSocketPathOverride("")
		dbusutil.SetSessionAddressOverride("")
		screen.SetDisplayOverride("")
		window.SetDisplayOverride("")
		input.SetDisplayOverride("")
	}()

	// Apply explicit session environment into opts for backends that accept it
	if env.xdgRuntimeDir != "" {
		opts.XDGRuntimeDir = env.xdgRuntimeDir
	}
	if env.waylandDisplay != "" {
		opts.WaylandDisplay = env.waylandDisplay
	}
	if env.dbusSessionAddress != "" {
		opts.DBusSessionAddress = env.dbusSessionAddress
	}

	pf := &Perfuncted{}
	var errs []error

	sc, err := screen.Open()
	if err != nil {
		errs = append(errs, fmt.Errorf("screen: %w", err))
	} else {
		pf.Screen = ScreenBundle{Screenshotter: sc}
	}

	maxX, maxY := opts.MaxX, opts.MaxY
	if maxX == 0 {
		maxX = 1920 // Default to 1080p; set MaxX/MaxY to your screen resolution for uinput.
	}
	if maxY == 0 {
		maxY = 1080
	}
	inp, err := input.Open(maxX, maxY)
	if err != nil {
		errs = append(errs, fmt.Errorf("input: %w", err))
	} else {
		pf.Input = InputBundle{Inputter: inp}
	}

	wm, err := window.Open()
	if err != nil {
		errs = append(errs, fmt.Errorf("window: %w", err))
	} else {
		pf.Window = WindowBundle{Manager: wm}
	}

	cb, err := clipboard.Open()
	if err != nil {
		errs = append(errs, fmt.Errorf("clipboard: %w", err))
	} else {
		pf.Clipboard = ClipboardBundle{Clipboard: cb}
	}

	if pf.Screen.Screenshotter == nil && pf.Input.Inputter == nil && pf.Window.Manager == nil {
		return nil, fmt.Errorf("perfuncted: no backend available: %w", errors.Join(errs...))
	}
	return pf, nil
}

// Close releases all backend resources.
func (pf *Perfuncted) Close() error {
	var errs []error
	if pf.Screen.Screenshotter != nil {
		if err := pf.Screen.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if pf.Input.Inputter != nil {
		if err := pf.Input.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if pf.Window.Manager != nil {
		if err := pf.Window.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if pf.Clipboard.Clipboard != nil {
		if err := pf.Clipboard.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RetryUntil calls fn repeatedly (at most every poll) until it returns nil or
// ctx is cancelled. The last non-nil error from fn is returned if the context
// expires.
func RetryUntil(ctx context.Context, poll time.Duration, fn func() error) error {
	var lastErr error
	for {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry: timed out: %w", lastErr)
		case <-time.After(poll):
		}
	}
}
