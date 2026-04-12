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
}

// ScreenBundle wraps a screen.Screenshotter with additional find utilities.
type ScreenBundle struct {
	screen.Screenshotter
}

// GrabHash captures a region and returns its pixel hash.
func (s ScreenBundle) GrabHash(rect image.Rectangle) (uint32, error) {
	if s.Screenshotter == nil {
		return 0, fmt.Errorf("screen: not available")
	}
	return find.GrabHash(s.Screenshotter, rect, nil)
}

// WaitForChange polls rect every poll interval until its hash differs from initial.
func (s ScreenBundle) WaitForChange(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	if s.Screenshotter == nil {
		return 0, fmt.Errorf("screen: not available")
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
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

// InputBundle wraps an input.Inputter with higher-level workflow methods.
type InputBundle struct {
	input.Inputter
}

// DoubleClick moves to (x, y) and performs two quick left clicks.
func (i InputBundle) DoubleClick(x, y int) error {
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	if err := i.MouseClick(x, y, 1); err != nil {
		return err
	}
	return i.MouseClick(x, y, 1)
}

// DragAndDrop moves to (x1, y1), presses left button, moves to (x2, y2), and releases.
func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
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
	if i.Inputter == nil {
		return fmt.Errorf("input: not available")
	}
	cx := rect.Min.X + rect.Dx()/2
	cy := rect.Min.Y + rect.Dy()/2
	return i.MouseClick(cx, cy, 1)
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
func New(opts Options) (*Perfuncted, error) {
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
