// Package input provides keyboard and mouse injection backends.
// Backend priority: wl-virtual (wlroots Wayland) → uinput (other Wayland) → XTEST (X11) → uinput (fallback).
// uinput requires membership in the "input" group or a udev rule:
//
// KERNEL=="uinput", GROUP="input", MODE="0660"
package input

import (
	"context"
	"fmt"
	"os"
	"sync"

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
	KeyDown(ctx context.Context, key string) error
	// KeyUp releases a previously held key.
	KeyUp(ctx context.Context, key string) error
	// KeyTap presses and immediately releases a key.
	KeyTap(ctx context.Context, key string) error
	// Type sends a string as a sequence of key events.
	Type(ctx context.Context, s string) error
	// MouseMove moves the pointer to absolute coordinates (x, y).
	MouseMove(ctx context.Context, x, y int) error
	// Click moves to (x, y) and clicks the given button.
	MouseClick(ctx context.Context, x, y, button int) error
	// MouseDown presses (but does not release) a mouse button.
	MouseDown(ctx context.Context, button int) error
	// MouseUp releases a previously pressed mouse button.
	MouseUp(ctx context.Context, button int) error
	// ScrollUp scrolls the mouse wheel up by the given number of notches.
	ScrollUp(ctx context.Context, clicks int) error
	// ScrollDown scrolls the mouse wheel down by the given number of notches.
	ScrollDown(ctx context.Context, clicks int) error
	// ScrollLeft scrolls the mouse wheel left by the given number of notches.
	ScrollLeft(ctx context.Context, clicks int) error
	// ScrollRight scrolls the mouse wheel right by the given number of notches.
	ScrollRight(ctx context.Context, clicks int) error
	// PressCombo sends a key combination, e.g. "ctrl+shift+t".
	PressCombo(ctx context.Context, combo string) error
	// Close releases all backend resources.
	Close() error
}

// Cached backend detection results
var (
	cachedInput   Inputter
	cachedInputMu sync.Mutex
	prevInputEnv  string
)

// Open returns the best available Inputter. Uses caching to avoid repeated
// backend detection and prioritizes backends based on detected session type.
func Open(maxX, maxY int32) (Inputter, error) {
	// Check cache first and invalidate if environment changed.
	currentFP := inputEnvFingerprint()
	cachedInputMu.Lock()
	if cachedInput != nil {
		if prevInputEnv == currentFP {
			defer cachedInputMu.Unlock()
			return cachedInput, nil
		}
		// Env changed -> close and clear cache
		cachedInput.Close()
		cachedInput = nil
	}
	cachedInputMu.Unlock()

	// Allow forcing a particular backend for debugging in CI/local runs.
	if os.Getenv("PF_FORCE_INPUT") == "uinput" {
		if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
			if b, err := NewUinputBackend(maxX, maxY); err == nil {
				cachedInputMu.Lock()
				cachedInput = b
				prevInputEnv = currentFP
				cachedInputMu.Unlock()
				return b, nil
			}
			return nil, fmt.Errorf("forced uinput selected but failed to initialize")
		}
		return nil, fmt.Errorf("forced uinput selected but /dev/uinput not accessible")
	}

	// On Wayland: prefer WlInputMethod (commit_string) when available because
	// it delivers Unicode text reliably to Wayland clients. Fallback order:
	//
	//	WlInputMethod -> WlVirtual -> uinput -> XTest
	if sock := wl.SocketPath(); sock != "" {
		// Try input method first
		if b, err := NewWlInputMethodBackend(sock, maxX, maxY); err == nil {
			cachedInputMu.Lock()
			cachedInput = b
			prevInputEnv = currentFP
			cachedInputMu.Unlock()
			return b, nil
		}
		// Then try wl-virtual (wlroots-specific)
		if b, err := NewWlVirtualBackend(sock); err == nil {
			cachedInputMu.Lock()
			cachedInput = b
			prevInputEnv = currentFP
			cachedInputMu.Unlock()
			return b, nil
		}
		// WlVirtual unavailable (e.g. KDE Plasma). uinput is preferred over
		// XTest here because XTest does not deliver events to native Wayland apps.
		if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
			if b, err := NewUinputBackend(maxX, maxY); err == nil {
				cachedInputMu.Lock()
				cachedInput = b
				prevInputEnv = currentFP
				cachedInputMu.Unlock()
				return b, nil
			}
		}
	}

	// Pure X11 or XWayland: XTest is scoped to the target display.
	if d := displayEnv(); d != "" {
		if b, err := NewXTestBackend(d); err == nil {
			cachedInputMu.Lock()
			cachedInput = b
			prevInputEnv = currentFP
			cachedInputMu.Unlock()
			return b, nil
		}
	}

	// Final fallback: uinput on systems without a Wayland session.
	if _, err := os.Stat("/dev/uinput"); err == nil {
		if b, err := NewUinputBackend(maxX, maxY); err == nil {
			cachedInputMu.Lock()
			cachedInput = b
			prevInputEnv = currentFP
			cachedInputMu.Unlock()
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
	d := displayEnv()
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

var displayOverride string

// SetDisplayOverride sets an explicit DISPLAY value for package-level lookups.
func SetDisplayOverride(d string) { displayOverride = d }

func displayEnv() string {
	if displayOverride != "" {
		return displayOverride
	}
	return os.Getenv("DISPLAY")
}

func inputEnvFingerprint() string {
	return os.Getenv("XDG_RUNTIME_DIR") + "|" + os.Getenv("WAYLAND_DISPLAY") + "|" + os.Getenv("DBUS_SESSION_BUS_ADDRESS") + "|" + displayEnv()
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
