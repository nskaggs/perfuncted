//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/internal/keymap"
)

var _ Inputter = (*UinputBackend)(nil)

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
	kb         uinput.Keyboard
	touchpad   uinput.TouchPad
	mouse      uinput.Mouse // lazy-initialised on first scroll
	charToRune map[rune]kernelChar
}

// kernelChar maps a rune to its evdev keycode and shift requirement
// using the active kernel keymap.
type kernelChar struct {
	keycode int
	shift   bool
}

// NewUinputBackend opens /dev/uinput and creates virtual keyboard and touchpad devices.
// maxX and maxY should be the screen width and height in pixels so absolute
// mouse coordinates map correctly.
// Returns an error with a hint when the device exists but permission is denied.
//
// Text typing is layout-independent: the kernel keymap is queried at init to
// determine which evdev keycode + shift state produces each character.
// Falls back to a static US QWERTY map if the kernel keymap is inaccessible.
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

	charToRune, err := buildKernelRuneMap()
	if err != nil {
		charToRune = qwertyRuneMap()
	}

	return &UinputBackend{kb: kb, touchpad: tp, charToRune: charToRune}, nil
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

func (b *UinputBackend) resolveKey(key string) (int, error) {
	if k, ok := keymap.FromString(key); ok {
		if code, ok := keyCode[k]; ok {
			return code, nil
		}
	}
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

func (b *UinputBackend) Type(ctx context.Context, s string) error {
	return b.TypeContext(ctx, s)
}

func (b *UinputBackend) TypeContext(ctx context.Context, s string) error {
	actions, err := ParseKeySend(s)
	if err != nil {
		return err
	}
	for _, a := range actions {
		if a.text != "" {
			if err := b.typeText(a.text); err != nil {
				return err
			}
			continue
		}
		if a.key == "" {
			continue
		}
		code, err := b.resolveKey(a.key)
		if err != nil {
			return err
		}
		if a.modifiers.shift {
			if err := b.kb.KeyDown(uinput.KeyLeftshift); err != nil {
				return err
			}
		}
		if a.modifiers.ctrl {
			if err := b.kb.KeyDown(uinput.KeyLeftctrl); err != nil {
				return err
			}
		}
		if a.modifiers.alt {
			if err := b.kb.KeyDown(uinput.KeyLeftalt); err != nil {
				return err
			}
		}
		if a.modifiers.super {
			if err := b.kb.KeyDown(uinput.KeyLeftmeta); err != nil {
				return err
			}
		}
		if a.down {
			if err := b.kb.KeyDown(code); err != nil {
				return err
			}
		} else {
			if err := b.kb.KeyPress(code); err != nil {
				return err
			}
		}
		if a.modifiers.super {
			if err := b.kb.KeyUp(uinput.KeyLeftmeta); err != nil {
				return err
			}
		}
		if a.modifiers.alt {
			if err := b.kb.KeyUp(uinput.KeyLeftalt); err != nil {
				return err
			}
		}
		if a.modifiers.ctrl {
			if err := b.kb.KeyUp(uinput.KeyLeftctrl); err != nil {
				return err
			}
		}
		if a.modifiers.shift {
			if err := b.kb.KeyUp(uinput.KeyLeftshift); err != nil {
				return err
			}
		}
	}
	return nil
}

// typeText types literal text character-by-character using the kernel keymap
// to determine the correct evdev keycode and shift state for each rune.
// This is layout-independent: on AZERTY 'a' is at KEY_Q position, on QWERTY it's KEY_A, etc.
func (b *UinputBackend) typeText(s string) error {
	for _, ch := range s {
		kc, ok := b.charToRune[ch]
		if !ok {
			return fmt.Errorf("input/uinput: unsupported character %q (not found in kernel keymap)", string(ch))
		}
		if kc.shift {
			if err := b.kb.KeyDown(uinput.KeyLeftshift); err != nil {
				return err
			}
		}
		if err := b.kb.KeyPress(kc.keycode); err != nil {
			if kc.shift {
				_ = b.kb.KeyUp(uinput.KeyLeftshift)
			}
			return err
		}
		if kc.shift {
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
		if err := b.ensureMouse(); err != nil {
			return err
		}
		return b.mouse.MiddlePress()
	case 3:
		return b.touchpad.RightPress()
	default:
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

func (b *UinputBackend) ScrollUp(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(false, int32(-clicks))
}

func (b *UinputBackend) ScrollDown(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(false, int32(clicks))
}

func (b *UinputBackend) ScrollLeft(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(true, int32(-clicks))
}

func (b *UinputBackend) ScrollRight(ctx context.Context, clicks int) error {
	if err := b.ensureMouse(); err != nil {
		return err
	}
	return b.mouse.Wheel(true, int32(clicks))
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

// ── Kernel keymap query (layout-independent rune → keycode mapping) ───────────

// kbEntry matches struct kbentry from <linux/kd.h>.
type kbEntry struct {
	table uint8
	index uint8
	value uint16
}

const (
	kdgkbent  = 0x4B46 // KDGKBENT ioctl
	kNormal   = 0x00   // K_NORMTAB
	kShift    = 0x01   // K_SHIFTTAB
	kAltGr    = 0x02   // K_ALTTAB
	kAltShift = 0x03   // K_ALTSHIFTTAB
)

// Kernel keysym types (from <linux/keyboard.h>).
const (
	ktLatin  = 0  // KT_LATIN  — plain ASCII/Latin character
	ktLetter = 11 // KT_LETTER — letter affected by CapsLock
)

// kernelRune extracts a Unicode rune from a kernel keysym value if it
// represents a typeable Latin/letter character, and reports whether the
// extraction succeeded.
func kernelRune(sym uint16) (rune, bool) {
	typ := sym >> 8
	switch typ {
	case ktLatin, ktLetter:
		return rune(sym & 0xFF), true
	}
	return 0, false
}

// buildKernelRuneMap reads the kernel keymap via KDGKBENT ioctl on a virtual
// console device and builds a reverse map from rune → (evdev keycode,
// needsShift). This makes typeText layout-independent: on AZERTY, 'a' maps
// to the KEY_Q evdev code; on QWERTY, 'a' maps to KEY_A.
//
// If no console device is accessible, falls back to the static US QWERTY map.
func buildKernelRuneMap() (map[rune]kernelChar, error) {
	// Try virtual console devices.  We need one the user has read access to.
	// /dev/ttyN for an active VC is typically readable by the user on that VC.
	paths := []string{}
	// Add /dev/tty0 first (current VC), then scan for accessible ttyN.
	paths = append(paths, "/dev/tty0")
	for i := 1; i <= 63; i++ {
		paths = append(paths, fmt.Sprintf("/dev/tty%d", i))
	}

	var f *os.File
	for _, p := range paths {
		if fh, err := os.OpenFile(p, os.O_RDONLY, 0); err == nil {
			// Verify the ioctl actually works on this device.
			ent := kbEntry{table: kNormal, index: 16} // KEY_Q
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fh.Fd(), kdgkbent, uintptr(unsafe.Pointer(&ent)))
			if errno == 0 {
				f = fh
				break
			}
			fh.Close()
		}
	}
	if f == nil {
		return nil, fmt.Errorf("no accessible virtual console for KDGKBENT")
	}
	defer f.Close()

	m := make(map[rune]kernelChar)

	// Scan keycodes 0–127 at the normal (unshifted) and shift tables.
	for kc := 0; kc < 128; kc++ {
		for _, table := range []uint8{kNormal, kShift} {
			ent := kbEntry{table: table, index: uint8(kc)}
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				f.Fd(),
				kdgkbent,
				uintptr(unsafe.Pointer(&ent)),
			)
			if errno != 0 {
				continue
			}
			r, ok := kernelRune(ent.value)
			if !ok || r == 0 {
				continue
			}
			// Prefer unshifted entry (first seen wins since we scan kNormal first).
			if _, exists := m[r]; !exists {
				m[r] = kernelChar{
					keycode: kc,
					shift:   table == kShift,
				}
			}
		}
	}

	if len(m) == 0 {
		return nil, fmt.Errorf("KDGKBENT returned no typeable entries")
	}

	return m, nil
}

// qwertyRuneMap returns a static US QWERTY rune → keycode map as fallback
// when the kernel keymap cannot be queried.
func qwertyRuneMap() map[rune]kernelChar {
	return map[rune]kernelChar{
		' ':  {uinput.KeySpace, false},
		'\t': {uinput.KeyTab, false},
		'\n': {uinput.KeyEnter, false},
		'0':  {uinput.Key0, false}, '1': {uinput.Key1, false}, '2': {uinput.Key2, false},
		'3': {uinput.Key3, false}, '4': {uinput.Key4, false}, '5': {uinput.Key5, false},
		'6': {uinput.Key6, false}, '7': {uinput.Key7, false}, '8': {uinput.Key8, false},
		'9': {uinput.Key9, false},
		'!': {uinput.Key1, true}, '@': {uinput.Key2, true}, '#': {uinput.Key3, true},
		'$': {uinput.Key4, true}, '%': {uinput.Key5, true}, '^': {uinput.Key6, true},
		'&': {uinput.Key7, true}, '*': {uinput.Key8, true}, '(': {uinput.Key9, true},
		')': {uinput.Key0, true},
		'a': {uinput.KeyA, false}, 'b': {uinput.KeyB, false}, 'c': {uinput.KeyC, false},
		'd': {uinput.KeyD, false}, 'e': {uinput.KeyE, false}, 'f': {uinput.KeyF, false},
		'g': {uinput.KeyG, false}, 'h': {uinput.KeyH, false}, 'i': {uinput.KeyI, false},
		'j': {uinput.KeyJ, false}, 'k': {uinput.KeyK, false}, 'l': {uinput.KeyL, false},
		'm': {uinput.KeyM, false}, 'n': {uinput.KeyN, false}, 'o': {uinput.KeyO, false},
		'p': {uinput.KeyP, false}, 'q': {uinput.KeyQ, false}, 'r': {uinput.KeyR, false},
		's': {uinput.KeyS, false}, 't': {uinput.KeyT, false}, 'u': {uinput.KeyU, false},
		'v': {uinput.KeyV, false}, 'w': {uinput.KeyW, false}, 'x': {uinput.KeyX, false},
		'y': {uinput.KeyY, false}, 'z': {uinput.KeyZ, false},
		'A': {uinput.KeyA, true}, 'B': {uinput.KeyB, true}, 'C': {uinput.KeyC, true},
		'D': {uinput.KeyD, true}, 'E': {uinput.KeyE, true}, 'F': {uinput.KeyF, true},
		'G': {uinput.KeyG, true}, 'H': {uinput.KeyH, true}, 'I': {uinput.KeyI, true},
		'J': {uinput.KeyJ, true}, 'K': {uinput.KeyK, true}, 'L': {uinput.KeyL, true},
		'M': {uinput.KeyM, true}, 'N': {uinput.KeyN, true}, 'O': {uinput.KeyO, true},
		'P': {uinput.KeyP, true}, 'Q': {uinput.KeyQ, true}, 'R': {uinput.KeyR, true},
		'S': {uinput.KeyS, true}, 'T': {uinput.KeyT, true}, 'U': {uinput.KeyU, true},
		'V': {uinput.KeyV, true}, 'W': {uinput.KeyW, true}, 'X': {uinput.KeyX, true},
		'Y': {uinput.KeyY, true}, 'Z': {uinput.KeyZ, true},
		'-': {uinput.KeyMinus, false}, '=': {uinput.KeyEqual, false},
		'[': {uinput.KeyLeftbrace, false}, ']': {uinput.KeyRightbrace, false},
		'\\': {uinput.KeyBackslash, false}, ';': {uinput.KeySemicolon, false},
		'\'': {uinput.KeyApostrophe, false}, '`': {uinput.KeyGrave, false},
		',': {uinput.KeyComma, false}, '.': {uinput.KeyDot, false},
		'/': {uinput.KeySlash, false},
		'_': {uinput.KeyMinus, true}, '+': {uinput.KeyEqual, true},
		'{': {uinput.KeyLeftbrace, true}, '}': {uinput.KeyRightbrace, true},
		'|': {uinput.KeyBackslash, true}, ':': {uinput.KeySemicolon, true},
		'"': {uinput.KeyApostrophe, true}, '~': {uinput.KeyGrave, true},
		'<': {uinput.KeyComma, true}, '>': {uinput.KeyDot, true}, '?': {uinput.KeySlash, true},
	}
}
