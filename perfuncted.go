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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
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
	// session in /tmp/perfuncted-xdg-* and switch the process environment to
	// target that session instead of the host desktop.
	Nested bool

	// XDGRuntimeDir, WaylandDisplay, and DBusSessionAddress allow callers to
	// specify the session environment directly instead of relying on (or
	// mutating) the process environment. When any of these are set, New()
	// calls os.Setenv before opening backends. This is the preferred way to
	// connect to a specific session — use it instead of calling os.Setenv
	// manually.
	XDGRuntimeDir      string
	WaylandDisplay     string
	DBusSessionAddress string
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
	return s.Grab(image.Rect(0, 0, w, h))
}

// GrabFullHash captures the entire output and returns its pixel hash.
func (s ScreenBundle) GrabFullHash() (uint32, error) {
	w, h, err := s.Resolution()
	if err != nil {
		return 0, err
	}
	return s.GrabHash(image.Rect(0, 0, w, h))
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
	initial, err := find.GrabHash(s.Screenshotter, rect, nil)
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
	return find.FindColor(s.Screenshotter, rect, target, tolerance)
}

// WindowBundle wraps a window.Manager with additional find utilities.
type WindowBundle struct {
	window.Manager
}

// Activate focuses the first window whose title contains pattern (case-insensitive).
// Note: This operates on a "first match wins" basis.
func (w WindowBundle) Activate(pattern string) error {
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

// FindByTitle returns the first window whose title contains pattern
// (case-insensitive). Use this to inspect window geometry or title before
// activating; Activate discards the match result.
func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	windows, err := w.Manager.List()
	if err != nil {
		return window.Info{}, err
	}
	lower := strings.ToLower(pattern)
	for _, win := range windows {
		if strings.Contains(strings.ToLower(win.Title), lower) {
			return win, nil
		}
	}
	return window.Info{}, fmt.Errorf("window: no window title matched %q", pattern)
}

// IsVisible reports whether any window whose title contains pattern
// (case-insensitive) is currently open.
func (w WindowBundle) IsVisible(pattern string) bool {
	_, err := w.FindByTitle(pattern)
	return err == nil
}

// WaitFor polls the window list until a window whose title contains pattern
// (case-insensitive) appears, or ctx is cancelled. List() errors are propagated
// rather than silently swallowed.
func (w WindowBundle) WaitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	lower := strings.ToLower(pattern)
	for {
		wins, err := w.Manager.List()
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
		wins, err := w.Manager.List()
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
	current, err := w.Manager.ActiveTitle()
	if err != nil {
		return "", fmt.Errorf("window: active title: %w", err)
	}
	for {
		title, err := w.Manager.ActiveTitle()
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
	if err := i.MouseClick(x, y, 1); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
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

// ClickCenter moves to the center of rect and performs a left click.
func (i InputBundle) ClickCenter(rect image.Rectangle) error {
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
	if combo == "" {
		return fmt.Errorf("input: PressCombo: combo must not be empty")
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

// Perfuncted bundles auto-detected screen, input, window, and clipboard backends.
type Perfuncted struct {
	Screen    ScreenBundle
	Input     InputBundle
	Window    WindowBundle
	Clipboard clipboard.Clipboard
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

	// Apply explicit session environment if provided.
	if opts.XDGRuntimeDir != "" {
		os.Setenv("XDG_RUNTIME_DIR", opts.XDGRuntimeDir)
	}
	if opts.WaylandDisplay != "" {
		os.Setenv("WAYLAND_DISPLAY", opts.WaylandDisplay)
	}
	if opts.DBusSessionAddress != "" {
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", opts.DBusSessionAddress)
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
		pf.Clipboard = cb
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
	if pf.Clipboard != nil {
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
