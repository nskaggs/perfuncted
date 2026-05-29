// Package input provides keyboard and mouse injection backends.
// Backend priority on Wayland: WlInputMethod -> wl-virtual -> XTEST (when
// DISPLAY is set) -> uinput. On X11, XTEST is used first; uinput remains the
// final fallback.
// uinput requires membership in the "input" group or a udev rule:
//
// KERNEL=="uinput", GROUP="input", MODE="0660"
package input

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

var newWlInputMethodBackend = func(sock string, maxX, maxY int32) (Inputter, error) {
	return NewWlInputMethodBackend(sock, maxX, maxY)
}

var newWlVirtualBackend = func(sock string) (Inputter, error) {
	return NewWlVirtualBackend(sock)
}

var newUinputBackend = func(maxX, maxY int32) (Inputter, error) {
	return NewUinputBackend(maxX, maxY)
}

var newXTestBackend = func(display string) (Inputter, error) {
	return NewXTestBackend(display)
}

var statUinput = func() error {
	_, err := os.Stat("/dev/uinput")
	return err
}

// ErrNotSupported is returned when the selected input backend cannot perform an operation.
var ErrNotSupported = errors.New("input: operation not supported on this backend")

func unsupportedError(backend, operation string) error {
	return fmt.Errorf("%s: %s: %w", backend, operation, ErrNotSupported)
}

func hasDelegatingBackend(b Inputter) bool {
	im, ok := b.(*WlInputMethodBackend)
	return !ok || im.other != nil
}

// Inputter injects keyboard and mouse events.
//
// Keyboard methods accept key names ("a", "ctrl", "return", "f5", etc.).
// Type accepts a key syntax: literal text is typed as-is, {keyname} sends
// named keys, modifier+key sends combinations, and {keyname down/up}
// holds/releases a key.
// Mouse methods use screen-absolute pixel coordinates; button 1=left,
// 2=middle, 3=right. Scroll methods accept a positive click count.
type Inputter interface {
	// KeyDown presses and holds a key.
	KeyDown(ctx context.Context, key string) error
	// KeyUp releases a previously held key.
	KeyUp(ctx context.Context, key string) error
	// Type sends a string as a sequence of key events using key syntax.
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
	// PointerLocation returns the current pointer location if the backend can query it.
	PointerLocation(ctx context.Context) (x, y int, err error)
	// Sync flushes any pending backend state when supported.
	Sync(ctx context.Context) error
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
		if statErr := statUinput(); statErr == nil {
			if b, err := newUinputBackend(maxX, maxY); err == nil {
				return b, nil
			} else {
				return nil, fmt.Errorf("forced uinput selected: %w", err)
			}
		}
		return nil, fmt.Errorf("forced uinput selected but /dev/uinput not accessible")
	}

	// On Wayland, prefer the input-method path for text-heavy apps. It handles
	// committed UTF-8 text directly and still delegates pointer/key combo
	// operations to a compositor-scoped backend when available. Fallback order:
	//
	//	WlInputMethod -> wl-virtual -> XTEST (if DISPLAY set) -> uinput
	if sock := rt.SocketPath(); sock != "" {
		if b, err := newWlInputMethodBackend(sock, maxX, maxY); err == nil {
			if hasDelegatingBackend(b) {
				return b, nil
			}
			_ = b.Close()
		}
		if b, err := newWlVirtualBackend(sock); err == nil {
			return b, nil
		}
		if d := rt.Display(); d != "" {
			if b, err := newXTestBackend(d); err == nil {
				return b, nil
			}
		}
		// wl-virtual/XTEST unavailable; uinput is the last Wayland fallback
		// when the compositor-scoped backends are unavailable.
		if statErr := statUinput(); statErr == nil {
			if b, err := newUinputBackend(maxX, maxY); err == nil {
				return b, nil
			}
		}
	}

	// Pure X11 or XWayland: XTest is scoped to the target display.
	if d := rt.Display(); d != "" {
		if b, err := newXTestBackend(d); err == nil {
			return b, nil
		}
	}

	// Final fallback: uinput on systems without a Wayland session.
	if err := statUinput(); err == nil {
		if b, err := newUinputBackend(maxX, maxY); err == nil {
			return b, nil
		} else {
			// Return uinput error directly—it includes permission hints.
			return nil, err
		}
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
	if sock := rt.SocketPath(); sock != "" {
		globs := wl.ListGlobals(sock)
		results := []probe.Result{
			checkWlInputMethodWithGlobs(sock, globs),
			checkWlVirtualWithGlobs(sock, globs),
		}
		if rt.Display() != "" {
			results = append(results, checkXTest(rt))
		}
		results = append(results, checkUinput())
		return probe.SelectBest(results)
	}
	return probe.SelectBest([]probe.Result{
		checkXTest(rt),
		checkUinput(),
	})
}

func checkWlInputMethod(rt env.Runtime) probe.Result {
	sock := rt.SocketPath()
	if sock == "" {
		return probe.Result{Name: "wl-input-method", Reason: "WAYLAND_DISPLAY not set"}
	}
	return checkWlInputMethodWithGlobs(sock, wl.ListGlobals(sock))
}

func checkWlInputMethodWithGlobs(sock string, globs map[string]bool) probe.Result {
	r := probe.Result{Name: "wl-input-method"}
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
	b, err := newXTestBackend(d)
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
	sock := rt.SocketPath()
	if sock == "" {
		return probe.Result{Name: "wl-virtual", Reason: "WAYLAND_DISPLAY not set"}
	}
	return checkWlVirtualWithGlobs(sock, wl.ListGlobals(sock))
}

func checkWlVirtualWithGlobs(sock string, globs map[string]bool) probe.Result {
	r := probe.Result{Name: "wl-virtual"}
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
