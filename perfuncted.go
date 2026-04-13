// Package perfuncted is a cross-platform screen automation library for Linux
// desktops. It auto-detects the best available backend for screen capture,
// input injection, and window management across X11, XWayland, and native
// Wayland sessions (wlroots, KDE, GNOME).
//
// Quick start:
//
// pf, err := perfuncted.New(perfuncted.Options{MaxX: 1920, MaxY: 1080})
// if err != nil { log.Fatal(err) }
// defer pf.Close()
//
// img, _ := pf.Screen.Grab(image.Rect(0, 0, 100, 100))
// _ = pf.Input.MouseMove(960, 540)
// _ = pf.Window.Activate("Firefox")
package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// Options controls backend selection.
type Options struct {
	// MaxX and MaxY define the absolute coordinate space for uinput's touch-pad
	// device. Set these to your primary monitor's resolution.
	MaxX, MaxY int32

	// Nested, when true, causes New() to auto-detect a nested perfuncted sway
	// session in /tmp/perfuncted-xdg-* and switch the process environment to
	// target that session instead of the host desktop.
	Nested bool
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
	return find.GrabHash(s.Screenshotter, rect, nil)
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
	before, err := find.GrabHash(s.Screenshotter, rect, nil)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

// FirstPixel returns the colour of the top-left pixel of rect.
func (s ScreenBundle) FirstPixel(rect image.Rectangle) (color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	return find.FirstPixel(s.Screenshotter, rect)
}

// LastPixel returns the colour of the bottom-right pixel of rect.
func (s ScreenBundle) LastPixel(rect image.Rectangle) (color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	return find.LastPixel(s.Screenshotter, rect)
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
	return find.LocateExact(s.Screenshotter, searchArea, reference)
}

// WaitWithTolerance waits for targetHash to appear within radius pixels of expectedRect.
func (s ScreenBundle) WaitWithTolerance(ctx context.Context, expectedRect image.Rectangle, targetHash uint32, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, image.Rectangle{}, err
	}
	return find.WaitWithTolerance(ctx, s.Screenshotter, expectedRect, targetHash, radius, poll, nil)
}

// WindowBundle wraps a window.Manager with additional find utilities.
type WindowBundle struct {
	window.Manager
}

// ActivateBy focuses the first window whose title contains pattern (case-insensitive).
// Note: This operates on a "first match wins" basis.
func (w WindowBundle) ActivateBy(pattern string) error {
	windows, err := w.Manager.List()
	if err != nil {
		return err
	}
	patternLower := strings.ToLower(pattern)
	for _, win := range windows {
		if strings.Contains(strings.ToLower(win.Title), patternLower) {
			return w.Manager.Activate(win.Title)
		}
	}
	return fmt.Errorf("window: no window title matched %q", pattern)
}

// WaitFor polls the window list until a window whose title contains pattern
// (case-insensitive) appears, or ctx is cancelled.
func (w WindowBundle) WaitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	lower := strings.ToLower(pattern)
	for {
		wins, _ := w.Manager.List()
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

// DoubleClick moves to (x, y) and performs two quick left clicks.
func (i InputBundle) DoubleClick(x, y int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.MouseClick(x, y, 1); err != nil {
		return err
	}
	return i.MouseClick(x, y, 1)
}

// DragAndDrop moves to (x1, y1), presses left button, moves to (x2, y2), and releases.
func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.MouseMove(x1, y1); err != nil {
		return err
	}
	if err := i.MouseDown(1); err != nil {
		return err
	}
	if err := i.MouseMove(x2, y2); err != nil {
		i.MouseUp(1) // best effort release
		return err
	}
	return i.MouseUp(1)
}

// ClickRectCenter moves to the center of rect and performs a left click.
func (i InputBundle) ClickRectCenter(rect image.Rectangle) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	cx := rect.Min.X + rect.Dx()/2
	cy := rect.Min.Y + rect.Dy()/2
	return i.MouseClick(cx, cy, 1)
}

// PressCombo sends a key combination like "ctrl+s" or "alt+f4".
// Modifiers are held down in order, the final key is tapped, then
// modifiers are released in reverse order.
func (i InputBundle) PressCombo(combo string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	parts := strings.Split(strings.ToLower(combo), "+")
	modifiers := parts[:len(parts)-1]
	final := parts[len(parts)-1]
	for _, m := range modifiers {
		if err := i.KeyDown(m); err != nil {
			return err
		}
	}
	if err := i.KeyTap(final); err != nil {
		for _, m := range modifiers {
			i.KeyUp(m) //nolint:errcheck
		}
		return err
	}
	for ix := len(modifiers) - 1; ix >= 0; ix-- {
		if err := i.KeyUp(modifiers[ix]); err != nil {
			return err
		}
	}
	return nil
}

// Perfuncted bundles auto-detected screen, input, and window backends.
type Perfuncted struct {
	Screen ScreenBundle
	Input  InputBundle
	Window WindowBundle
}

// New opens all backends using auto-detection. Each backend is attempted
// independently; an error from one does not prevent the others from opening.
// Returns an error only when no backend could be opened at all.
//
// When opts.Nested is true, New() auto-detects a perfuncted nested sway session
// in /tmp/perfuncted-xdg-* and switches the process environment to target it.
func New(opts Options) (*Perfuncted, error) {
	if opts.Nested {
		xdg, wd, dbus, err := NestedEnv()
		if err != nil {
			return nil, fmt.Errorf("perfuncted: nested session: %w", err)
		}
		os.Setenv("XDG_RUNTIME_DIR", xdg)
		os.Setenv("WAYLAND_DISPLAY", wd)
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", dbus)
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
	return errors.Join(errs...)
}
