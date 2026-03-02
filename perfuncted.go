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
	"errors"
	"fmt"

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

// Perfuncted bundles auto-detected screen, input, and window backends.
type Perfuncted struct {
	Screen screen.Screenshotter
	Input  input.Inputter
	Window window.Manager
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
		pf.Screen = sc
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
		pf.Input = inp
	}

	wm, err := window.Open()
	if err != nil {
		errs = append(errs, fmt.Errorf("window: %w", err))
	} else {
		pf.Window = wm
	}

	if pf.Screen == nil && pf.Input == nil && pf.Window == nil {
		return nil, fmt.Errorf("perfuncted: no backend available: %w", errors.Join(errs...))
	}
	return pf, nil
}

// Close releases all backend resources.
func (pf *Perfuncted) Close() error {
	var errs []error
	if pf.Screen != nil {
		if err := pf.Screen.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if pf.Input != nil {
		if err := pf.Input.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if pf.Window != nil {
		if err := pf.Window.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
