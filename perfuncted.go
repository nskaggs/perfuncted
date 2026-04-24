// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
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
	MaxX, MaxY int32
	Nested     bool

	XDGRuntimeDir      string
	WaylandDisplay     string
	DBusSessionAddress string
}

type sessionEnv struct {
	xdgRuntimeDir      string
	waylandDisplay     string
	dbusSessionAddress string
}

func resolveSessionEnv(opts Options) (sessionEnv, error) {
	env := sessionEnv{
		xdgRuntimeDir:      opts.XDGRuntimeDir,
		waylandDisplay:     opts.WaylandDisplay,
		dbusSessionAddress: opts.DBusSessionAddress,
	}

	if opts.Nested {
		xdg, wl, dbus, err := NestedEnv()
		if err != nil {
			return env, err
		}
		env.xdgRuntimeDir = xdg
		env.waylandDisplay = wl
		env.dbusSessionAddress = dbus
	}
	return env, nil
}

func applySessionEnv(env sessionEnv) func() {
	origXDG := os.Getenv("XDG_RUNTIME_DIR")
	origWL := os.Getenv("WAYLAND_DISPLAY")
	origDBus := os.Getenv("DBUS_SESSION_BUS_ADDRESS")

	if env.xdgRuntimeDir != "" {
		os.Setenv("XDG_RUNTIME_DIR", env.xdgRuntimeDir)
	}
	if env.waylandDisplay != "" {
		os.Setenv("WAYLAND_DISPLAY", env.waylandDisplay)
	}
	if env.dbusSessionAddress != "" {
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", env.dbusSessionAddress)
	}

	return func() {
		os.Setenv("XDG_RUNTIME_DIR", origXDG)
		os.Setenv("WAYLAND_DISPLAY", origWL)
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", origDBus)
	}
}

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

func DetectSession() (kind string, details map[string]string) {
	details = make(map[string]string)
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	wd := os.Getenv("WAYLAND_DISPLAY")

	if strings.HasPrefix(xdg, "/tmp/perfuncted-xdg-") {
		details["dir"] = xdg
		details["wayland_display"] = wd
		details["dbus_address"] = os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		return "nested", details
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

func (s ScreenBundle) GrabHash(rect image.Rectangle) (uint32, error) {
	return s.GrabHashContext(context.Background(), rect)
}

func (s ScreenBundle) GrabHashContext(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	if rect.Empty() || rect == image.Rect(0, 0, 0, 0) {
		return s.Screenshotter.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.Screenshotter, rect, nil)
}

func (s ScreenBundle) GrabFullHash() (uint32, error) {
	return s.GrabFullHashContext(context.Background())
}

func (s ScreenBundle) GrabFullHashContext(ctx context.Context) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return s.Screenshotter.GrabFullHash(ctx)
}

func (s ScreenBundle) Grab(rect image.Rectangle) (image.Image, error) {
	return s.GrabContext(context.Background(), rect)
}

func (s ScreenBundle) GrabContext(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return s.Screenshotter.Grab(ctx, rect)
}

func (s ScreenBundle) GrabFull() (image.Image, error) {
	return s.GrabFullContext(context.Background())
}

func (s ScreenBundle) GrabFullContext(ctx context.Context) (image.Image, error) {
	return s.GrabContext(ctx, image.Rectangle{})
}

func (s ScreenBundle) CaptureRegion(rect image.Rectangle, path string) error {
	img, err := s.GrabContext(context.Background(), rect)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (s ScreenBundle) GetPixel(x, y int) (color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	c, err := find.FirstPixel(context.Background(), s.Screenshotter, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, err
	}
	return c, nil
}

func (s ScreenBundle) GetMultiplePixels(points []image.Point) ([]color.RGBA, error) {
	return s.GetMultiplePixelsContext(context.Background(), points)
}

func (s ScreenBundle) GetMultiplePixelsContext(ctx context.Context, points []image.Point) ([]color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	out := make([]color.RGBA, len(points))
	for i, p := range points {
		c, err := find.FirstPixel(ctx, s.Screenshotter, image.Rect(p.X, p.Y, p.X+1, p.Y+1))
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}

func (s ScreenBundle) PixelToScreen(rect image.Rectangle) (int, int, error) {
	return rect.Min.X, rect.Min.Y, nil
}

func (s ScreenBundle) WaitForFn(rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	return s.WaitForFnContext(context.Background(), rect, fn, poll)
}

func (s ScreenBundle) WaitForFnContext(ctx context.Context, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

func (s ScreenBundle) WaitForVisibleChange(rect image.Rectangle, poll time.Duration, stable ...int) (uint32, error) {
	return s.WaitForVisibleChangeContext(context.Background(), rect, poll, stable...)
}

func (s ScreenBundle) WaitForVisibleChangeContext(ctx context.Context, rect image.Rectangle, poll time.Duration, stable ...int) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	initial, err := s.GrabHashContext(ctx, rect)
	if err != nil {
		return 0, err
	}
	h, err := find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
	if err != nil {
		return 0, err
	}
	if len(stable) > 0 && stable[0] > 1 {
		return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable[0], poll, nil)
	}
	return h, nil
}

func (s ScreenBundle) WaitForStable(rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	return s.WaitForStableContext(context.Background(), rect, stableN, poll)
}

func (s ScreenBundle) WaitForStableContext(ctx context.Context, rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stableN, poll, nil)
}

func (s ScreenBundle) WaitForSettle(rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	return s.WaitForSettleContext(context.Background(), rect, action, stable, poll)
}

func (s ScreenBundle) WaitForSettleContext(ctx context.Context, rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := s.GrabHashContext(ctx, rect)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) LocateExact(rect image.Rectangle, reference image.Image) (image.Rectangle, error) {
	return s.LocateExactContext(context.Background(), rect, reference)
}

func (s ScreenBundle) LocateExactContext(ctx context.Context, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.LocateExact(ctx, s.Screenshotter, searchArea, reference)
}

func (s ScreenBundle) WaitForLocate(rect image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	return s.WaitForLocateContext(context.Background(), rect, reference, poll)
}

func (s ScreenBundle) WaitForLocateContext(ctx context.Context, searchArea image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.WaitForLocate(ctx, s.Screenshotter, searchArea, reference, poll)
}

func (s ScreenBundle) WaitWithTolerance(rect image.Rectangle, want uint32, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	return s.WaitWithToleranceContext(context.Background(), rect, want, radius, poll)
}

func (s ScreenBundle) WaitWithToleranceContext(ctx context.Context, rect image.Rectangle, want uint32, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, image.Rectangle{}, err
	}
	return find.WaitWithTolerance(ctx, s.Screenshotter, rect, want, radius, poll, nil)
}

func (s ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	return s.FindColorContext(context.Background(), rect, target, tolerance)
}

func (s ScreenBundle) FindColorContext(ctx context.Context, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(ctx, s.Screenshotter, rect, target, tolerance)
}

func (s ScreenBundle) WaitForNoChange(rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	return s.WaitForNoChangeContext(context.Background(), rect, stable, poll)
}

func (s ScreenBundle) WaitForNoChangeContext(ctx context.Context, rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) WaitForChange(rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	return s.WaitForChangeContext(context.Background(), rect, initial, poll)
}

func (s ScreenBundle) WaitForChangeContext(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

func (s ScreenBundle) WaitFor(rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	return s.WaitForContext(context.Background(), rect, want, poll)
}

func (s ScreenBundle) WaitForContext(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

func (s ScreenBundle) ScanFor(rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	return s.ScanForContext(context.Background(), rects, wants, poll)
}

func (s ScreenBundle) ScanForContext(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

func (s ScreenBundle) Resolution() (int, int, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.Resolution(s.Screenshotter)
}

// InputBundle wraps per-compositor input backends.
type InputBundle struct {
	input.Inputter
}

func (i InputBundle) checkAvailable() error {
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	return nil
}

func (i InputBundle) Type(text string) error {
	return i.TypeContext(context.Background(), text)
}

func (i InputBundle) TypeContext(ctx context.Context, text string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.Type(ctx, text)
}

func (i InputBundle) TypeWithDelay(text string, delay time.Duration) error {
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	for _, r := range text {
		if err := i.Inputter.Type(context.Background(), string(r)); err != nil {
			return err
		}
		time.Sleep(delay)
	}
	return nil
}

func (i InputBundle) KeyTap(key string) error {
	return i.KeyTapContext(context.Background(), key)
}

func (i InputBundle) KeyTapContext(ctx context.Context, key string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyTap(ctx, key)
}

func (i InputBundle) KeyDown(key string) error {
	return i.KeyDownContext(context.Background(), key)
}

func (i InputBundle) KeyDownContext(ctx context.Context, key string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyDown(ctx, key)
}

func (i InputBundle) KeyUp(key string) error {
	return i.KeyUpContext(context.Background(), key)
}

func (i InputBundle) KeyUpContext(ctx context.Context, key string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyUp(ctx, key)
}

func (i InputBundle) PressCombo(combo string) error {
	return i.PressComboContext(context.Background(), combo)
}

func (i InputBundle) PressComboContext(ctx context.Context, combo string) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.PressCombo(ctx, combo)
}

func (i InputBundle) MouseClick(x, y, button int) error {
	return i.MouseClickContext(context.Background(), x, y, button)
}

func (i InputBundle) MouseClickContext(ctx context.Context, x, y, button int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseClick(ctx, x, y, button)
}

func (i InputBundle) ClickCenter(rect image.Rectangle) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	x, y := rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2
	return i.Inputter.MouseClick(context.Background(), x, y, 1)
}

func (i InputBundle) DoubleClick(x, y int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.Inputter.MouseMove(context.Background(), x, y); err != nil {
		return err
	}
	if err := i.Inputter.MouseClick(context.Background(), x, y, 1); err != nil {
		return err
	}
	return i.Inputter.MouseClick(context.Background(), x, y, 1)
}

func (i InputBundle) MouseMove(x, y int) error {
	return i.MouseMoveContext(context.Background(), x, y)
}

func (i InputBundle) MouseMoveContext(ctx context.Context, x, y int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseMove(ctx, x, y)
}

func (i InputBundle) Move(x, y int) error {
	return i.MouseMove(x, y)
}

func (i InputBundle) MouseDown(button int) error {
	return i.MouseDownContext(context.Background(), button)
}

func (i InputBundle) MouseDownContext(ctx context.Context, button int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseDown(ctx, button)
}

func (i InputBundle) MouseUp(button int) error {
	return i.MouseUpContext(context.Background(), button)
}

func (i InputBundle) MouseUpContext(ctx context.Context, button int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseUp(ctx, button)
}

func (i InputBundle) ScrollUp(clicks int) error {
	return i.ScrollUpContext(context.Background(), clicks)
}

func (i InputBundle) ScrollUpContext(ctx context.Context, clicks int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollUp(ctx, clicks)
}

func (i InputBundle) ScrollDown(clicks int) error {
	return i.ScrollDownContext(context.Background(), clicks)
}

func (i InputBundle) ScrollDownContext(ctx context.Context, clicks int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollDown(ctx, clicks)
}

func (i InputBundle) ScrollLeft(clicks int) error {
	return i.ScrollLeftContext(context.Background(), clicks)
}

func (i InputBundle) ScrollLeftContext(ctx context.Context, clicks int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollLeft(ctx, clicks)
}

func (i InputBundle) ScrollRight(clicks int) error {
	return i.ScrollRightContext(context.Background(), clicks)
}

func (i InputBundle) ScrollRightContext(ctx context.Context, clicks int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollRight(ctx, clicks)
}

func (i InputBundle) Scroll(dx, dy int) error {
	if dx > 0 {
		return i.ScrollRight(dx)
	} else if dx < 0 {
		return i.ScrollLeft(-dx)
	}
	if dy > 0 {
		return i.ScrollUp(dy)
	} else if dy < 0 {
		return i.ScrollDown(-dy)
	}
	return nil
}

func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.Inputter.MouseMove(context.Background(), x1, y1); err != nil {
		return err
	}
	if err := i.Inputter.MouseDown(context.Background(), 1); err != nil {
		return err
	}
	if err := i.Inputter.MouseMove(context.Background(), x2, y2); err != nil {
		return err
	}
	return i.Inputter.MouseUp(context.Background(), 1)
}

func (i InputBundle) ModifierDown(mod string) error {
	return i.inputter().KeyDown(context.Background(), mod)
}
func (i InputBundle) ModifierUp(mod string) error {
	return i.inputter().KeyUp(context.Background(), mod)
}

func (i InputBundle) inputter() input.Inputter { return i.Inputter }

func (i InputBundle) Raw(event string) error { return nil }

// WindowBundle wraps window management utilities.
type WindowBundle struct {
	window.Manager
}

func (w WindowBundle) checkAvailable() error {
	if w.Manager == nil {
		return fmt.Errorf("window: not available")
	}
	return nil
}

func (w WindowBundle) Activate(pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(context.Background(), pattern)
}

func (w WindowBundle) ActiveTitle() (string, error) {
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(context.Background())
}

func (w WindowBundle) CloseWindow(pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(context.Background(), pattern)
}

func (w WindowBundle) Resize(pattern string, width, height int) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(context.Background(), pattern, width, height)
}

func (w WindowBundle) Minimize(pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(context.Background(), pattern)
}

func (w WindowBundle) Maximize(pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(context.Background(), pattern)
}

func (w WindowBundle) Restore(pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(context.Background(), pattern)
}

func (w WindowBundle) WaitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.WaitFor(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) WaitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return window.WaitForClose(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) WaitForTitleChange(ctx context.Context, poll time.Duration) (string, error) {
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	initial, err := w.Manager.ActiveTitle(ctx)
	if err != nil {
		return "", err
	}

	const (
		maxPoll      = 100 * time.Millisecond
		jitterFactor = 0.1
	)

	delay := poll
	if delay > maxPoll {
		delay = maxPoll
	}

	for {
		current, err := w.Manager.ActiveTitle(ctx)
		if err == nil && current != initial {
			return current, nil
		}

		// Calculate next delay with exponential backoff and jitter
		nextDelay := delay * 2
		if nextDelay > maxPoll {
			nextDelay = maxPoll
		}
		jitter := time.Duration(float64(nextDelay) * jitterFactor * (rand.Float64()*2 - 1))
		nextDelay += jitter
		if nextDelay < time.Millisecond {
			nextDelay = time.Millisecond
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
			delay = nextDelay
		}
	}
}

func (w WindowBundle) IsVisible(pattern string) bool {
	if err := w.checkAvailable(); err != nil {
		return false
	}
	_, err := window.FindByTitle(context.Background(), w.Manager, pattern)
	return err == nil
}

func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(context.Background(), w.Manager, pattern)
}

func (w WindowBundle) GetGeometry(pattern string) (image.Rectangle, error) {
	if err := w.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	info, err := window.FindByTitle(context.Background(), w.Manager, pattern)
	if err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H), nil
}

func (w WindowBundle) GetProcess(pattern string) (int, error) {
	if err := w.checkAvailable(); err != nil {
		return 0, err
	}
	info, err := window.FindByTitle(context.Background(), w.Manager, pattern)
	if err != nil {
		return 0, err
	}
	return int(info.PID), nil
}

// ClipboardBundle wraps the clipboard interface.
type ClipboardBundle struct {
	clipboard.Clipboard
}

func (c ClipboardBundle) checkAvailable() error {
	if c.Clipboard == nil {
		return fmt.Errorf("clipboard: not available")
	}
	return nil
}

func (c ClipboardBundle) Get() (string, error) {
	if err := c.checkAvailable(); err != nil {
		return "", err
	}
	return c.Clipboard.Get(context.Background())
}

func (c ClipboardBundle) Set(text string) error {
	if err := c.checkAvailable(); err != nil {
		return err
	}
	return c.Clipboard.Set(context.Background(), text)
}

func (c ClipboardBundle) PasteWithInput(text string, inp InputBundle) error {
	if err := c.Set(text); err != nil {
		return err
	}
	return inp.PressCombo("ctrl+v")
}

// Perfuncted is the top-level session handle.
type Perfuncted struct {
	Screen    ScreenBundle
	Input     InputBundle
	Window    WindowBundle
	Clipboard ClipboardBundle
}

func (p *Perfuncted) Paste(text string) error {
	return p.Clipboard.PasteWithInput(text, p.Input)
}

func New(opts Options) (*Perfuncted, error) {
	env, err := resolveSessionEnv(opts)
	if err != nil {
		return nil, err
	}
	restore := applySessionEnv(env)
	defer restore()

	if os.Getenv("DISPLAY") != "" {
		screen.SetDisplayOverride(os.Getenv("DISPLAY"))
	}

	// Eagerly open the screen backend to preserve prior behaviour for callers
	// that expect screen availability immediately.
	scr, err := screen.Open()
	if err != nil {
		return nil, err
	}

	// Defer initialization of input/window/clipboard backends until first use.
	inpLazy := &lazyInputter{
		env:  env,
		maxX: opts.MaxX,
		maxY: opts.MaxY,
	}
	winLazy := &lazyWindowManager{env: env}
	cbLazy := &lazyClipboard{env: env}

	return &Perfuncted{
		Screen:    ScreenBundle{scr},
		Input:     InputBundle{inpLazy},
		Window:    WindowBundle{winLazy},
		Clipboard: ClipboardBundle{cbLazy},
	}, nil
}

func (p *Perfuncted) Close() error {
	var errs []error
	if p.Screen.Screenshotter != nil {
		errs = append(errs, p.Screen.Screenshotter.Close())
	}
	if p.Input.Inputter != nil {
		errs = append(errs, p.Input.Close())
	}
	if p.Window.Manager != nil {
		errs = append(errs, p.Window.Close())
	}
	if p.Clipboard.Clipboard != nil {
		errs = append(errs, p.Clipboard.Close())
	}
	var lastErr error
	for _, e := range errs {
		if e != nil {
			lastErr = e
		}
	}
	return lastErr
}

func Retry(ctx context.Context, poll time.Duration, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry: timed out: %w", err)
		case <-time.After(poll):
		}
	}
}
