// Package input provides keyboard and mouse injection backends.
// Backend priority on Wayland: WlInputMethod -> wl-virtual -> uinput.
// On X11, XTEST is used first; uinput remains the final fallback.
// uinput requires membership in the "input" group or a udev rule:
//
// KERNEL=="uinput", GROUP="input", MODE="0660"
package input

import (
	"context"
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted/internal/env"
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

// Open returns the best available Inputter for the current process environment.
func Open(maxX, maxY int32) (Inputter, error) {
	return OpenRuntime(env.Current(), maxX, maxY)
}

// OpenRuntime returns the best available Inputter for rt.
func OpenRuntime(rt env.Runtime, maxX, maxY int32) (Inputter, error) {
	// Allow forcing a particular backend for debugging in CI/local runs.
	if os.Getenv("PF_FORCE_INPUT") == "uinput" {
		if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
			if b, err := NewUinputBackend(maxX, maxY); err == nil {
				return b, nil
			}
			return nil, fmt.Errorf("forced uinput selected but failed to initialize")
		}
		return nil, fmt.Errorf("forced uinput selected but /dev/uinput not accessible")
	}

	// On Wayland, prefer the input-method path for text-heavy apps. It handles
	// committed UTF-8 text directly and still delegates pointer/key combo
	// operations to a compositor-scoped backend when available. Fallback order:
	//
	//	WlInputMethod -> wl-virtual -> uinput
	if sock := rt.SocketPath(); sock != "" {
		if b, err := NewWlInputMethodBackend(sock, maxX, maxY); err == nil {
			return b, nil
		}
		if b, err := NewWlVirtualBackend(sock); err == nil {
			return b, nil
		}
		// wl-virtual unavailable (e.g. compositor lacks the extension). uinput is the last
		// Wayland fallback when the compositor-scoped backends are unavailable.
		if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
			if b, err := NewUinputBackend(maxX, maxY); err == nil {
				return b, nil
			}
		}
	}

	// Pure X11 or XWayland: XTest is scoped to the target display.
	if d := rt.Display(); d != "" {
		if b, err := NewXTestBackend(d); err == nil {
			return b, nil
		}
	}

	// Final fallback: uinput on systems without a Wayland session.
	if _, err := os.Stat("/dev/uinput"); err == nil {
		if b, err := NewUinputBackend(maxX, maxY); err == nil {
			return b, nil
		}
		// Return uinput error directly—it includes permission hints.
		return nil, err
	}

	return nil, fmt.Errorf("input: no backend available (uinput inaccessible, DISPLAY not set)")
}

// Probe returns availability details for each input backend in the same
// priority order that Open() uses for the current session type.
func Probe() []probe.Result {
	return ProbeRuntime(env.Current())
}

// ProbeRuntime returns availability details for rt.
func ProbeRuntime(rt env.Runtime) []probe.Result {
	if rt.SocketPath() != "" {
		return probe.SelectBest([]probe.Result{
			checkWlInputMethod(rt),
			checkWlVirtual(rt),
			checkUinput(),
		})
	}
	return probe.SelectBest([]probe.Result{
		checkXTest(rt),
		checkUinput(),
	})
}

func checkWlInputMethod(rt env.Runtime) probe.Result {
	r := probe.Result{Name: "wl-input-method"}
	sock := rt.SocketPath()
	if sock == "" {
		r.Reason = "WAYLAND_DISPLAY not set"
		return r
	}
	globs := wl.ListGlobals(sock)
	if globs == nil {
		r.Reason = fmt.Sprintf("connect %s: failed", sock)
		return r
	}
	if !globs["zwp_input_method_manager_v2"] {
		r.Reason = "zwp_input_method_manager_v2 not advertised"
		return r
	}
	if !globs["wl_seat"] {
		r.Reason = "wl_seat not advertised"
		return r
	}
	r.Available = true
	r.Reason = "zwp_input_method_manager_v2 + wl_seat available"
	return r
}

func checkXTest(rt env.Runtime) probe.Result {
	r := probe.Result{Name: "xtest"}
	d := rt.Display()
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

func checkWlVirtual(rt env.Runtime) probe.Result {
	r := probe.Result{Name: "wl-virtual"}
	sock := rt.SocketPath()
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
