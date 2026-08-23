//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/internal/contextutil"
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

	// lifecycleMu protects closed, closeDone, closeErr, and active.
	// It is never held while device I/O runs.
	lifecycleMu sync.Mutex
	closed      bool
	closeDone   chan struct{}
	closeErr    error
	active      map[uint64]context.CancelFunc
	activeDone  chan struct{}
	nextActive  uint64

	// operationGate serializes each complete public input operation so device
	// event writes cannot interleave with another logical operation.
	gateOnce      sync.Once
	operationGate chan struct{}
	// mouseMu owns lazy mouse creation, use, and pointer replacement during
	// teardown. It is separate from lifecycleMu to keep lock ordering clear.
	mouseMu sync.Mutex
}

var errUinputBackendClosed = errors.New("input/uinput: backend is closed")

var createUinputMouse = uinput.CreateMouse

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

func (b *UinputBackend) operationGateChannel() chan struct{} {
	b.gateOnce.Do(func() {
		b.operationGate = make(chan struct{}, 1)
		b.operationGate <- struct{}{}
	})
	return b.operationGate
}

func (b *UinputBackend) beginOperation(ctx context.Context) (context.Context, func(), error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if b == nil {
		return nil, nil, errors.New("input/uinput: backend is nil")
	}

	opCtx, cancel := context.WithCancel(ctx)
	b.lifecycleMu.Lock()
	if b.closed {
		b.lifecycleMu.Unlock()
		cancel()
		return nil, nil, errUinputBackendClosed
	}
	if b.active == nil {
		b.active = make(map[uint64]context.CancelFunc)
	}
	if len(b.active) == 0 {
		b.activeDone = make(chan struct{})
	}
	b.nextActive++
	id := b.nextActive
	b.active[id] = cancel
	b.lifecycleMu.Unlock()

	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			cancel()
			b.lifecycleMu.Lock()
			delete(b.active, id)
			if len(b.active) == 0 && b.activeDone != nil {
				close(b.activeDone)
			}
			b.lifecycleMu.Unlock()
		})
	}
	return opCtx, finish, nil
}

func (b *UinputBackend) withOperation(ctx context.Context, fn func(context.Context) error) error {
	opCtx, finish, err := b.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer finish()

	gate := b.operationGateChannel()
	select {
	case <-opCtx.Done():
		return opCtx.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	if err := opCtx.Err(); err != nil {
		return err
	}
	return fn(opCtx)
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

// KeyDown presses and holds key through uinput.
func (b *UinputBackend) KeyDown(ctx context.Context, key string) error {
	return b.withOperation(ctx, func(context.Context) error {
		code, err := b.resolveKey(key)
		if err != nil {
			return err
		}
		return b.kb.KeyDown(code)
	})
}

// KeyUp releases key through uinput.
func (b *UinputBackend) KeyUp(ctx context.Context, key string) error {
	return b.withOperation(ctx, func(context.Context) error {
		code, err := b.resolveKey(key)
		if err != nil {
			return err
		}
		return b.kb.KeyUp(code)
	})
}

// Type sends text through uinput using the input key syntax.
func (b *UinputBackend) Type(ctx context.Context, s string) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.typeContext(ctx, s)
	})
}

// TypeLiteral sends text literally, without interpreting key syntax.
func (b *UinputBackend) TypeLiteral(ctx context.Context, s string) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.typeText(ctx, s)
	})
}

func (b *UinputBackend) typeContext(ctx context.Context, s string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	actions, err := parseKeySend(s)
	if err != nil {
		return err
	}
	for _, a := range actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.text != "" {
			if err := b.typeText(ctx, a.text); err != nil {
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
		if err := b.typeKeyWithMods(ctx, code, a.down, a.up, a.modifiers); err != nil {
			return err
		}
	}
	return nil
}

// typeKeyWithMods presses modifier keys, sends the key action, then releases
// modifiers in reverse order. If any step fails, already-pressed modifiers
// are released (best-effort) before the error is returned.
func (b *UinputBackend) typeKeyWithMods(ctx context.Context, code int, down, up bool, mods modifiers) error { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	// Build ordered list of modifier keycodes.
	var modKeys []int
	if mods.shift {
		modKeys = append(modKeys, uinput.KeyLeftshift)
	}
	if mods.ctrl {
		modKeys = append(modKeys, uinput.KeyLeftctrl)
	}
	if mods.alt {
		modKeys = append(modKeys, uinput.KeyLeftalt)
	}
	if mods.super {
		modKeys = append(modKeys, uinput.KeyLeftmeta)
	}

	// Press modifiers; release any already-pressed ones on failure.
	pressed := 0
	releaseHeld := func() {
		for i := pressed - 1; i >= 0; i-- {
			_ = b.kb.KeyUp(modKeys[i])
		}
	}
	for _, mk := range modKeys {
		if err := ctx.Err(); err != nil {
			releaseHeld()
			return err
		}
		if err := b.kb.KeyDown(mk); err != nil {
			releaseHeld()
			return err
		}
		pressed++
	}

	// Send the key.
	if err := ctx.Err(); err != nil {
		releaseHeld()
		return err
	}
	switch {
	case up:
		if err := b.kb.KeyUp(code); err != nil {
			releaseHeld()
			return err
		}
	case down:
		if err := b.kb.KeyDown(code); err != nil {
			releaseHeld()
			return err
		}
	default:
		if err := b.kb.KeyPress(code); err != nil {
			releaseHeld()
			return err
		}
	}

	// Release modifiers in reverse order.
	for i := len(modKeys) - 1; i >= 0; i-- {
		if err := b.kb.KeyUp(modKeys[i]); err != nil {
			for j := i - 1; j >= 0; j-- {
				_ = b.kb.KeyUp(modKeys[j])
			}
			return err
		}
	}
	return nil
}

// typeText types literal text character-by-character using the kernel keymap
// to determine the correct evdev keycode and shift state for each rune.
// This is layout-independent: on AZERTY 'a' is at KEY_Q position, on QWERTY it's KEY_A, etc.
func (b *UinputBackend) typeText(ctx context.Context, s string) error {
	ctx = contextutil.Default(ctx)
	for _, ch := range s {
		if err := ctx.Err(); err != nil {
			return err
		}
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

// MouseMove moves the pointer to absolute coordinates x and y.
func (b *UinputBackend) MouseMove(ctx context.Context, x, y int) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseMove(ctx, x, y)
	})
}

func (b *UinputBackend) mouseMove(ctx context.Context, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.touchpad.MoveTo(int32(x), int32(y))
}

// MouseClick moves to x and y and clicks button.
func (b *UinputBackend) MouseClick(ctx context.Context, x, y, button int) error {
	if err := validateMouseButton("input/uinput", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		if err := b.mouseMove(ctx, x, y); err != nil {
			return err
		}
		if err := b.mouseDown(ctx, button); err != nil {
			return err
		}
		if err := b.mouseUp(context.WithoutCancel(ctx), button); err != nil {
			return err
		}
		return ctx.Err()
	})
}

// MouseDown presses button.
func (b *UinputBackend) MouseDown(ctx context.Context, button int) error {
	if err := validateMouseButton("input/uinput", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseDown(ctx, button)
	})
}

func (b *UinputBackend) mouseDown(ctx context.Context, button int) error {
	if err := validateMouseButton("input/uinput", button); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch button {
	case 1:
		return b.touchpad.LeftPress()
	case 2:
		return b.withMouse(func(mouse uinput.Mouse) error {
			return mouse.MiddlePress()
		})
	case 3:
		return b.touchpad.RightPress()
	default:
		return validateMouseButton("input/uinput", button)
	}
}

// MouseUp releases button.
func (b *UinputBackend) MouseUp(ctx context.Context, button int) error {
	if err := validateMouseButton("input/uinput", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseUp(ctx, button)
	})
}

func (b *UinputBackend) mouseUp(ctx context.Context, button int) error {
	if err := validateMouseButton("input/uinput", button); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch button {
	case 1:
		return b.touchpad.LeftRelease()
	case 2:
		return b.withMouse(func(mouse uinput.Mouse) error {
			return mouse.MiddleRelease()
		})
	case 3:
		return b.touchpad.RightRelease()
	default:
		return validateMouseButton("input/uinput", button)
	}
}

func (b *UinputBackend) ensureMouse() error {
	b.mouseMu.Lock()
	defer b.mouseMu.Unlock()
	return b.ensureMouseLocked()
}

func (b *UinputBackend) ensureMouseLocked() error {
	if b.closedForMouse() {
		return errUinputBackendClosed
	}
	if b.mouse != nil {
		return nil
	}
	m, err := createUinputMouse("/dev/uinput", []byte("perfuncted-mouse"))
	if err != nil {
		return fmt.Errorf("input/uinput: create mouse for scroll: %w", err)
	}
	if m == nil {
		return errors.New("input/uinput: create mouse for scroll: factory returned nil device")
	}
	if b.closedForMouse() {
		if closeErr := m.Close(); closeErr != nil {
			return errors.Join(
				errUinputBackendClosed,
				fmt.Errorf("input/uinput: close mouse after backend close: %w", closeErr),
			)
		}
		return errUinputBackendClosed
	}
	b.mouse = m
	return nil
}

func (b *UinputBackend) closedForMouse() bool {
	b.lifecycleMu.Lock()
	closed := b.closed
	b.lifecycleMu.Unlock()
	return closed
}

func (b *UinputBackend) withMouse(fn func(uinput.Mouse) error) error {
	b.mouseMu.Lock()
	defer b.mouseMu.Unlock()
	if err := b.ensureMouseLocked(); err != nil {
		return err
	}
	return fn(b.mouse)
}

// ScrollUp scrolls upward by clicks.
func (b *UinputBackend) ScrollUp(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, false, -clicks, clicks)
	})
}

// ScrollDown scrolls downward by clicks.
func (b *UinputBackend) ScrollDown(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, false, clicks, clicks)
	})
}

// ScrollLeft scrolls left by clicks.
func (b *UinputBackend) ScrollLeft(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, true, -clicks, clicks)
	})
}

// ScrollRight scrolls right by clicks.
func (b *UinputBackend) ScrollRight(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, true, clicks, clicks)
	})
}

func (b *UinputBackend) scroll(ctx context.Context, horizontal bool, delta, clicks int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withMouse(func(mouse uinput.Mouse) error {
		return mouse.Wheel(horizontal, int32(delta))
	})
}

// PointerLocation returns the pointer coordinates when available.
func (b *UinputBackend) PointerLocation(ctx context.Context) (int, int, error) {
	err := b.withOperation(ctx, func(context.Context) error {
		return unsupportedError("input/uinput", "pointer location")
	})
	return 0, 0, err
}

// Sync flushes pending uinput events.
func (b *UinputBackend) Sync(ctx context.Context) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return ctx.Err()
	})
}

// Close marks the backend closed, waits for admitted operations to finish, and
// releases each uinput device exactly once.
func (b *UinputBackend) Close() error {
	if b == nil {
		return nil
	}

	b.lifecycleMu.Lock()
	if b.closed {
		done := b.closeDone
		b.lifecycleMu.Unlock()
		<-done
		b.lifecycleMu.Lock()
		err := b.closeErr
		b.lifecycleMu.Unlock()
		return err
	}
	b.closed = true
	b.closeDone = make(chan struct{})
	done := b.closeDone
	activeDone := b.activeDone
	cancels := make([]context.CancelFunc, 0, len(b.active))
	for _, cancel := range b.active {
		cancels = append(cancels, cancel)
	}
	b.lifecycleMu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if activeDone != nil {
		<-activeDone
	}

	var errs []error
	if b.kb != nil {
		if err := b.kb.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.touchpad != nil {
		if err := b.touchpad.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	b.mouseMu.Lock()
	m := b.mouse
	b.mouse = nil
	b.mouseMu.Unlock()
	if m != nil {
		if err := m.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	closeErr := errors.Join(errs...)

	b.lifecycleMu.Lock()
	b.closeErr = closeErr
	close(done)
	b.lifecycleMu.Unlock()
	return closeErr
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
