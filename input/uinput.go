//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/internal/keymap"
)

// UinputBackend injects keyboard and mouse events via /dev/uinput.
// It is compositor-agnostic and works on X11, XWayland, and all Wayland compositors.
//
// Permission: /dev/uinput typically requires group "input" membership or a udev rule:
//
//	KERNEL=="uinput", GROUP="input", MODE="0660"
//
// Sandboxed environments (Flatpak, Snap) may also block access.
//
// Mouse movement uses a virtual touchpad with absolute coordinates in the range
// [0, maxCoord]. Callers should pass the screen dimensions as maxX/maxY.
type UinputBackend struct {
	kb       uinput.Keyboard
	touchpad uinput.TouchPad
	mouse    uinput.Mouse // lazy-initialised on first scroll
}

// NewUinputBackend opens /dev/uinput and creates virtual keyboard and touchpad devices.
// maxX and maxY should be the screen width and height in pixels so absolute
// mouse coordinates map correctly.
// Returns an error with a hint when the device exists but permission is denied.
//
// WARNING: The Type() method assumes a US QWERTY keyboard layout when mapping
// characters to keycodes. Non-ASCII characters and keys in different positions
// on other layouts (e.g. AZERTY, Dvorak) will produce incorrect output.
// Use WlVirtualBackend or XTestBackend if layout-independent typing is required.
func NewUinputBackend(maxX, maxY int32) (*UinputBackend, error) {
	if _, err := os.Stat("/dev/uinput"); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("input/uinput: /dev/uinput not found; kernel module uinput may not be loaded")
	}

	kb, err := uinput.CreateKeyboard("/dev/uinput", []byte("perfuncted-keyboard"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("input/uinput: permission denied opening /dev/uinput; " +
				"add yourself to the 'input' group or create a udev rule: " +
				"KERNEL==\"uinput\", GROUP=\"input\", MODE=\"0660\"")
		}
		return nil, fmt.Errorf("input/uinput: create keyboard: %w", err)
	}

	tp, err := uinput.CreateTouchPad("/dev/uinput", []byte("perfuncted-touchpad"), 0, maxX, 0, maxY)
	if err != nil {
		_ = kb.Close()
		return nil, fmt.Errorf("input/uinput: create touchpad: %w", err)
	}

	return &UinputBackend{kb: kb, touchpad: tp}, nil
}

// keyCode maps generic Key identifiers to uinput codes.
// Use internal/keymap to resolve string names — this keeps naming consistent
// across backends.
var keyCode = map[keymap.Key]int{
	keymap.KeyA: uinput.KeyA, keymap.KeyB: uinput.KeyB, keymap.KeyC: uinput.KeyC, keymap.KeyD: uinput.KeyD,
	keymap.KeyE: uinput.KeyE, keymap.KeyF: uinput.KeyF, keymap.KeyG: uinput.KeyG, keymap.KeyH: uinput.KeyH,
	keymap.KeyI: uinput.KeyI, keymap.KeyJ: uinput.KeyJ, keymap.KeyK: uinput.KeyK, keymap.KeyL: uinput.KeyL,
	keymap.KeyM: uinput.KeyM, keymap.KeyN: uinput.KeyN, keymap.KeyO: uinput.KeyO, keymap.KeyP: uinput.KeyP,
	keymap.KeyQ: uinput.KeyQ, keymap.KeyR: uinput.KeyR, keymap.KeyS: uinput.KeyS, keymap.KeyT: uinput.KeyT,
	keymap.KeyU: uinput.KeyU, keymap.KeyV: uinput.KeyV, keymap.KeyW: uinput.KeyW, keymap.KeyX: uinput.KeyX,
	keymap.KeyY: uinput.KeyY, keymap.KeyZ: uinput.KeyZ,
	keymap.Key0: uinput.Key0, keymap.Key1: uinput.Key1, keymap.Key2: uinput.Key2, keymap.Key3: uinput.Key3,
	keymap.Key4: uinput.Key4, keymap.Key5: uinput.Key5, keymap.Key6: uinput.Key6, keymap.Key7: uinput.Key7,
	keymap.Key8: uinput.Key8, keymap.Key9: uinput.Key9,
	keymap.KeySpace:     uinput.KeySpace,
	keymap.KeyEnter:     uinput.KeyEnter,
	keymap.KeyTab:       uinput.KeyTab,
	keymap.KeyBackspace: uinput.KeyBackspace,
	keymap.KeyEscape:    uinput.KeyEsc,
	keymap.KeyCtrl:      uinput.KeyLeftctrl,
	keymap.KeyAlt:       uinput.KeyLeftalt,
	keymap.KeyShift:     uinput.KeyLeftshift,
	keymap.KeySuper:     uinput.KeyLeftmeta,
	keymap.KeyUp:        uinput.KeyUp,
	keymap.KeyDown:      uinput.KeyDown,
	keymap.KeyLeft:      uinput.KeyLeft,
	keymap.KeyRight:     uinput.KeyRight,
	keymap.KeyHome:      uinput.KeyHome,
	keymap.KeyEnd:       uinput.KeyEnd,
	keymap.KeyPageUp:    uinput.KeyPageup,
	keymap.KeyPageDown:  uinput.KeyPagedown,
	keymap.KeyInsert:    uinput.KeyInsert,
	keymap.KeyDelete:    uinput.KeyDelete,
	keymap.KeyF1:        uinput.KeyF1, keymap.KeyF2: uinput.KeyF2, keymap.KeyF3: uinput.KeyF3,
	keymap.KeyF4: uinput.KeyF4, keymap.KeyF5: uinput.KeyF5, keymap.KeyF6: uinput.KeyF6,
	keymap.KeyF7: uinput.KeyF7, keymap.KeyF8: uinput.KeyF8, keymap.KeyF9: uinput.KeyF9,
	keymap.KeyF10: uinput.KeyF10, keymap.KeyF11: uinput.KeyF11, keymap.KeyF12: uinput.KeyF12,
}

// charKey maps a printable rune to its uinput keycode and whether Shift is required.
// US QWERTY layout assumed.
type charKey struct {
	code  int
	shift bool
}

var charToKey = map[rune]charKey{
	// Whitespace
	' ':  {uinput.KeySpace, false},
	'\t': {uinput.KeyTab, false},
	'\n': {uinput.KeyEnter, false},
	// Digits
	'0': {uinput.Key0, false}, '1': {uinput.Key1, false}, '2': {uinput.Key2, false},
	'3': {uinput.Key3, false}, '4': {uinput.Key4, false}, '5': {uinput.Key5, false},
	'6': {uinput.Key6, false}, '7': {uinput.Key7, false}, '8': {uinput.Key8, false},
	'9': {uinput.Key9, false},
	// Shift+digit symbols
	'!': {uinput.Key1, true}, '@': {uinput.Key2, true}, '#': {uinput.Key3, true},
	'$': {uinput.Key4, true}, '%': {uinput.Key5, true}, '^': {uinput.Key6, true},
	'&': {uinput.Key7, true}, '*': {uinput.Key8, true}, '(': {uinput.Key9, true},
	')': {uinput.Key0, true},
	// Lowercase letters
	'a': {uinput.KeyA, false}, 'b': {uinput.KeyB, false}, 'c': {uinput.KeyC, false},
	'd': {uinput.KeyD, false}, 'e': {uinput.KeyE, false}, 'f': {uinput.KeyF, false},
	'g': {uinput.KeyG, false}, 'h': {uinput.KeyH, false}, 'i': {uinput.KeyI, false},
	'j': {uinput.KeyJ, false}, 'k': {uinput.KeyK, false}, 'l': {uinput.KeyL, false},
	'm': {uinput.KeyM, false}, 'n': {uinput.KeyN, false}, 'o': {uinput.KeyO, false},
	'p': {uinput.KeyP, false}, 'q': {uinput.KeyQ, false}, 'r': {uinput.KeyR, false},
	's': {uinput.KeyS, false}, 't': {uinput.KeyT, false}, 'u': {uinput.KeyU, false},
	'v': {uinput.KeyV, false}, 'w': {uinput.KeyW, false}, 'x': {uinput.KeyX, false},
	'y': {uinput.KeyY, false}, 'z': {uinput.KeyZ, false},
	// Uppercase letters
	'A': {uinput.KeyA, true}, 'B': {uinput.KeyB, true}, 'C': {uinput.KeyC, true},
	'D': {uinput.KeyD, true}, 'E': {uinput.KeyE, true}, 'F': {uinput.KeyF, true},
	'G': {uinput.KeyG, true}, 'H': {uinput.KeyH, true}, 'I': {uinput.KeyI, true},
	'J': {uinput.KeyJ, true}, 'K': {uinput.KeyK, true}, 'L': {uinput.KeyL, true},
	'M': {uinput.KeyM, true}, 'N': {uinput.KeyN, true}, 'O': {uinput.KeyO, true},
	'P': {uinput.KeyP, true}, 'Q': {uinput.KeyQ, true}, 'R': {uinput.KeyR, true},
	'S': {uinput.KeyS, true}, 'T': {uinput.KeyT, true}, 'U': {uinput.KeyU, true},
	'V': {uinput.KeyV, true}, 'W': {uinput.KeyW, true}, 'X': {uinput.KeyX, true},
	'Y': {uinput.KeyY, true}, 'Z': {uinput.KeyZ, true},
	// Punctuation (unshifted)
	'-': {uinput.KeyMinus, false}, '=': {uinput.KeyEqual, false},
	'[': {uinput.KeyLeftbrace, false}, ']': {uinput.KeyRightbrace, false},
	'\\': {uinput.KeyBackslash, false}, ';': {uinput.KeySemicolon, false},
	'\'': {uinput.KeyApostrophe, false}, '`': {uinput.KeyGrave, false},
	',': {uinput.KeyComma, false}, '.': {uinput.KeyDot, false},
	'/': {uinput.KeySlash, false},
	// Punctuation (shifted)
	'_': {uinput.KeyMinus, true}, '+': {uinput.KeyEqual, true},
	'{': {uinput.KeyLeftbrace, true}, '}': {uinput.KeyRightbrace, true},
	'|': {uinput.KeyBackslash, true}, ':': {uinput.KeySemicolon, true},
	'"': {uinput.KeyApostrophe, true}, '~': {uinput.KeyGrave, true},
	'<': {uinput.KeyComma, true}, '>': {uinput.KeyDot, true}, '?': {uinput.KeySlash, true},
}

func (b *UinputBackend) resolveKey(key string) (int, error) {
	// Try canonical map from internal/keymap.
	if k, ok := keymap.FromString(key); ok {
		if code, ok := keyCode[k]; ok {
			return code, nil
		}
	}
	// Single character fallback: lowercase
	if len(key) == 1 {
		if k, ok := keymap.FromString(strings.ToLower(key)); ok {
			if code, ok := keyCode[k]; ok {
				return code, nil
			}
		}
	}
	return 0, fmt.Errorf("input/uinput: unknown key %q", key)
}

func (b *UinputBackend) KeyDown(ctx context.Context, key string) error {
	code, err := b.resolveKey(key)
	if err != nil {
		return err
	}
	return b.kb.KeyDown(code)
}

func (b *UinputBackend) KeyUp(ctx context.Context, key string) error {
	code, err := b.resolveKey(key)
	if err != nil {
		return err
	}
	return b.kb.KeyUp(code)
}

func (b *UinputBackend) KeyTap(ctx context.Context, key string) error {
	code, err := b.resolveKey(key)
	if err != nil {
		return err
	}
	return b.kb.KeyPress(code)
}

func (b *UinputBackend) Type(ctx context.Context, s string) error {
	return b.TypeContext(ctx, s)
}

func (b *UinputBackend) TypeContext(ctx context.Context, s string) error {
	for _, ch := range s {
		ck, ok := charToKey[ch]
		if !ok {
			return fmt.Errorf("input/uinput: unsupported character %q", string(ch))
		}
		if ck.shift {
			if err := b.kb.KeyDown(uinput.KeyLeftshift); err != nil {
				return err
			}
		}
		if err := b.kb.KeyPress(ck.code); err != nil {
			if ck.shift {
				_ = b.kb.KeyUp(uinput.KeyLeftshift)
			}
			return err
		}
		if ck.shift {
			if err := b.kb.KeyUp(uinput.KeyLeftshift); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *UinputBackend) MouseMove(ctx context.Context, x, y int) error {
	return b.touchpad.MoveTo(int32(x), int32(y))
}

func (b *UinputBackend) MouseClick(ctx context.Context, x, y, button int) error {
	if err := b.MouseMove(ctx, x, y); err != nil {
		return err
	}
	if err := b.MouseDown(ctx, button); err != nil {
		return err
	}
	return b.MouseUp(ctx, button)
}

func (b *UinputBackend) MouseDown(ctx context.Context, button int) error {
	switch button {
	case 1:
		return b.touchpad.LeftPress()
	case 2:
		// Middle click requires a relative mouse device.
		if err := b.ensureMouse(); err != nil {
			return err
		}
		return b.mouse.MiddlePress()
	case 3:
		return b.touchpad.RightPress()
	default:
		// Try to provide better diagnostics: if a relative mouse can be created,
		// report that the specific button isn't implemented rather than claiming
		// the compositor/touchpad doesn't support it.
		if err := b.ensureMouse(); err != nil {
			return fmt.Errorf("input/uinput: unsupported mouse button %d (touchpad only supports left=1, right=3) and creating a relative mouse failed: %w", button, err)
		}
		return fmt.Errorf("input/uinput: unsupported mouse button %d (only 1=left,2=middle,3=right supported)", button)
	}
}

func (b *UinputBackend) MouseUp(ctx context.Context, button int) error {
	switch button {
	case 1:
		return b.touchpad.LeftRelease()
	case 2:
		// Middle release requires the relative mouse device.
		if err := b.ensureMouse(); err != nil {
			return err
		}
		return b.mouse.MiddleRelease()
	case 3:
		return b.touchpad.RightRelease()
	default:
		if err := b.ensureMouse(); err != nil {
			return fmt.Errorf("input/uinput: unsupported mouse button %d (touchpad only supports left=1, right=3) and creating a relative mouse failed: %w", button, err)
		}
		return fmt.Errorf("input/uinput: unsupported mouse button %d (only 1=left,2=middle,3=right supported)", button)
	}
}

func (b *UinputBackend) ensureMouse() error {
	if b.mouse != nil {
		return nil
	}
	m, err := uinput.CreateMouse("/dev/uinput", []byte("perfuncted-mouse"))
	if err != nil {
		return fmt.Errorf("input/uinput: create mouse for scroll: %w", err)
	}
	b.mouse = m
	return nil
}

// ScrollUp scrolls the mouse wheel up by the given number of notches.
func (b *UinputBackend) ScrollUp(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(false, int32(-clicks))
}

// ScrollDown scrolls the mouse wheel down by the given number of notches.
func (b *UinputBackend) ScrollDown(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(false, int32(clicks))
}

// ScrollLeft scrolls the mouse wheel left by the given number of notches.
func (b *UinputBackend) ScrollLeft(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(true, int32(-clicks))
}

// ScrollRight scrolls the mouse wheel right by the given number of notches.
func (b *UinputBackend) ScrollRight(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(true, int32(clicks))
}

func (b *UinputBackend) PressCombo(ctx context.Context, combo string) error {
	parts := strings.Split(strings.ToLower(combo), "+")
	codes := make([]int, 0, len(parts))
	for _, p := range parts {
		code, err := b.resolveKey(strings.TrimSpace(p))
		if err != nil {
			return err
		}
		codes = append(codes, code)
	}
	// Press all
	for _, c := range codes {
		if err := b.kb.KeyDown(c); err != nil {
			return err
		}
	}
	// Release in reverse
	for i := len(codes) - 1; i >= 0; i-- {
		if err := b.kb.KeyUp(codes[i]); err != nil {
			return err
		}
	}
	return nil
}

func (b *UinputBackend) Close() error {
	var errs []error
	if err := b.kb.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := b.touchpad.Close(); err != nil {
		errs = append(errs, err)
	}
	if b.mouse != nil {
		if err := b.mouse.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
