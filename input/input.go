// Package input provides keyboard and mouse injection backends.
// Backend priority: wl-virtual (wlroots Wayland) → uinput (other Wayland) → XTEST (X11) → uinput (fallback).
// uinput requires membership in the "input" group or a udev rule:
//
//	KERNEL=="uinput", GROUP="input", MODE="0660"
package input

import (
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Inputter injects keyboard and mouse events.
//
// Keyboard methods accept key names ("a", "ctrl", "return", "f5", etc.).
// Mouse methods use screen-absolute pixel coordinates; button 1=left,
// 2=middle, 3=right. Scroll methods accept a positive click count.
type Inputter interface {
	// KeyDown presses and holds a key.
	KeyDown(key string) error
	// KeyUp releases a previously held key.
	KeyUp(key string) error
	// KeyTap presses and immediately releases a key.
	KeyTap(key string) error
	// Type sends a string as a sequence of key events.
	Type(s string) error
	// MouseMove moves the pointer to absolute coordinates (x, y).
	MouseMove(x, y int) error
	// MouseClick moves to (x, y) and clicks the given button.
	MouseClick(x, y, button int) error
	// MouseDown presses (but does not release) a mouse button.
	MouseDown(button int) error
	// MouseUp releases a previously pressed mouse button.
	MouseUp(button int) error
	// ScrollUp scrolls the mouse wheel up by the given number of notches.
	ScrollUp(clicks int) error
	// ScrollDown scrolls the mouse wheel down by the given number of notches.
	ScrollDown(clicks int) error
	// ScrollLeft scrolls the mouse wheel left by the given number of notches.
	ScrollLeft(clicks int) error
	// ScrollRight scrolls the mouse wheel right by the given number of notches.
	ScrollRight(clicks int) error
	// Close releases all backend resources.
	Close() error
}

// Open returns the best available Inputter. On wlroots Wayland compositors
// (sway, Hyprland) the Wayland virtual input backend is tried first so that
// events use the compositor's own coordinate space. On other Wayland compositors
// (e.g. KDE Plasma, GNOME), uinput is preferred over XTEST because XTEST is
// scoped to X11/XWayland and does not deliver events to native Wayland windows.
func Open(maxX, maxY int32) (Inputter, error) {
	// On Wayland: prefer WlVirtual (wlroots-specific), then uinput (kernel-level,
	// reaches all Wayland windows), then XTest (X11/XWayland only).
	if sock := wl.SocketPath(); sock != "" {
		if b, err := NewWlVirtualBackend(sock); err == nil {
			return b, nil
		}
		// WlVirtual unavailable (e.g. KDE Plasma). uinput is preferred over
		// XTest here because XTest does not deliver events to native Wayland apps.
		if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
			if b, err := NewUinputBackend(maxX, maxY); err == nil {
				return b, nil
			}
		}
	}
	// Pure X11 or XWayland: XTest is scoped to the target display.
	if d := os.Getenv("DISPLAY"); d != "" {
		if b, err := NewXTestBackend(d); err == nil {
			return b, nil
		}
	}
	// Final fallback: uinput on systems without a Wayland session.
	if _, err := os.Stat("/dev/uinput"); err == nil {
		b, err := NewUinputBackend(maxX, maxY)
		if err == nil {
			return b, nil
		}
		// Return uinput error directly—it includes permission hints.
		return nil, err
	}
	return nil, fmt.Errorf("input: no backend available (uinput inaccessible, DISPLAY not set)")
}

// Probe returns availability details for each input backend, in the priority
// order that Open() uses: wl-virtual first (wlroots Wayland), then uinput
// (other Wayland compositors), then XTEST (X11/XWayland).
func Probe() []probe.Result {
	// On Wayland, uinput outranks XTEST (XTEST only reaches X11/XWayland).
	if wl.SocketPath() != "" {
		return probe.SelectBest([]probe.Result{
			checkWlVirtual(),
			checkUinput(),
			checkXTest(),
		})
	}
	return probe.SelectBest([]probe.Result{
		checkWlVirtual(),
		checkXTest(),
		checkUinput(),
	})
}

func checkXTest() probe.Result {
	r := probe.Result{Name: "xtest"}
	d := os.Getenv("DISPLAY")
	if d == "" {
		r.Reason = "DISPLAY not set"
		return r
	}
	b, err := NewXTestBackend(d)
	if err != nil {
		r.Reason = fmt.Sprintf("XTEST unavailable on %s: %v", d, err)
		return r
	}
	b.Close()
	r.Available = true
	r.Reason = fmt.Sprintf("XTEST available on %s", d)
	return r
}

func checkWlVirtual() probe.Result {
	r := probe.Result{Name: "wl-virtual"}
	sock := wl.SocketPath()
	if sock == "" {
		r.Reason = "WAYLAND_DISPLAY not set"
		return r
	}
	globs := wl.ListGlobals(sock)
	if globs == nil {
		r.Reason = fmt.Sprintf("connect %s: failed", sock)
		return r
	}
	if !globs["zwlr_virtual_pointer_manager_v1"] {
		r.Reason = "zwlr_virtual_pointer_manager_v1 not advertised"
		return r
	}
	if !globs["zwp_virtual_keyboard_manager_v1"] {
		r.Reason = "zwp_virtual_keyboard_manager_v1 not advertised"
		return r
	}
	r.Available = true
	r.Reason = "zwlr_virtual_pointer_manager_v1 + zwp_virtual_keyboard_manager_v1 available"
	return r
}

func checkUinput() probe.Result {
	r := probe.Result{Name: "uinput"}
	info, err := os.Stat("/dev/uinput")
	if os.IsNotExist(err) {
		r.Reason = "/dev/uinput not found — load the uinput kernel module"
		return r
	}
	if err != nil {
		r.Reason = fmt.Sprintf("/dev/uinput: %v", err)
		return r
	}
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		if os.IsPermission(err) {
			r.Reason = fmt.Sprintf("/dev/uinput exists (mode %v) but permission denied", info.Mode())
		} else {
			r.Reason = fmt.Sprintf("/dev/uinput open: %v", err)
		}
		return r
	}
	f.Close()
	r.Available = true
	r.Reason = fmt.Sprintf("/dev/uinput accessible (mode %v)", info.Mode())
	return r
}
