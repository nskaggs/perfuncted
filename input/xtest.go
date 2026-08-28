//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/x11"
)

var _ Inputter = (*XTestBackend)(nil)

// XTestBackend injects keyboard and mouse events via the X11 XTEST extension.
// It only works on X11 or XWayland sessions. Prefer UinputBackend when available.
type XTestBackend struct {
	conn  x11.Connection
	root  xproto.Window
	delay time.Duration

	// lifecycleMu protects closed, closeDone, closeErr, and active. It is
	// never held while connection I/O runs.
	lifecycleMu sync.Mutex
	closed      bool
	closeDone   chan struct{}
	closeErr    error
	active      map[uint64]context.CancelFunc
	activeDone  chan struct{}
	nextActive  uint64

	// operationGate serializes each complete public operation. Composite
	// operations call gate-free helpers so they cannot deadlock by re-entering
	// this boundary.
	operationGateOnce sync.Once
	operationGate     chan struct{}

	keymapOnce sync.Once
	keymap     map[xproto.Keysym]keycodeLevel
	keymapErr  error
}

type keycodeLevel struct {
	keycode xproto.Keycode
	level   int
}

var errXTestBackendClosed = errors.New("input/xtest: backend is closed")

// NewXTestBackend connects to the named X11 display and initialises XTEST.
// Pass an empty string to use the DISPLAY environment variable.
func NewXTestBackend(displayName string) (*XTestBackend, error) {
	conn, err := x11.NewXgbConnection(displayName)
	if err != nil {
		return nil, fmt.Errorf("input/xtest: connect to display %q: %w", displayName, err)
	}
	if err := conn.InitXTest(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("input/xtest: init XTEST: %w", err)
	}
	root := conn.DefaultScreen().Root
	return &XTestBackend{conn: conn, root: root, delay: 50 * time.Millisecond}, nil
}

func (b *XTestBackend) operationGateChannel() chan struct{} {
	b.operationGateOnce.Do(func() {
		b.operationGate = make(chan struct{}, 1)
		b.operationGate <- struct{}{}
	})
	return b.operationGate
}

func (b *XTestBackend) beginOperation(ctx context.Context) (context.Context, func(), error) {
	if b == nil {
		return nil, nil, errors.New("input/xtest: backend is nil")
	}
	opCtx, cancel := context.WithCancel(ctx)

	b.lifecycleMu.Lock()
	if b.closed {
		b.lifecycleMu.Unlock()
		cancel()
		return nil, nil, errXTestBackendClosed
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

	finish := sync.OnceFunc(func() {
		cancel()
		b.lifecycleMu.Lock()
		delete(b.active, id)
		if len(b.active) == 0 && b.activeDone != nil {
			close(b.activeDone)
		}
		b.lifecycleMu.Unlock()
	})
	return opCtx, finish, nil
}

func (b *XTestBackend) withOperation(ctx context.Context, fn func(context.Context) error) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
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

// keysymForName maps a key name to an X11 keysym value.
// Letters map to their correct keysyms (A→0x41, a→0x61); typeText queries
// the server keymap to determine which level each keysym lives at and holds
// Shift only when level >= 1.
var keysymForName = map[string]xproto.Keysym{
	"a": 0x61, "b": 0x62, "c": 0x63, "d": 0x64, "e": 0x65,
	"f": 0x66, "g": 0x67, "h": 0x68, "i": 0x69, "j": 0x6a,
	"k": 0x6b, "l": 0x6c, "m": 0x6d, "n": 0x6e, "o": 0x6f,
	"p": 0x70, "q": 0x71, "r": 0x72, "s": 0x73, "t": 0x74,
	"u": 0x75, "v": 0x76, "w": 0x77, "x": 0x78, "y": 0x79, "z": 0x7a,
	"A": 0x41, "B": 0x42, "C": 0x43, "D": 0x44, "E": 0x45,
	"F": 0x46, "G": 0x47, "H": 0x48, "I": 0x49, "J": 0x4a,
	"K": 0x4b, "L": 0x4c, "M": 0x4d, "N": 0x4e, "O": 0x4f,
	"P": 0x50, "Q": 0x51, "R": 0x52, "S": 0x53, "T": 0x54,
	"U": 0x55, "V": 0x56, "W": 0x57, "X": 0x58, "Y": 0x59, "Z": 0x5a,
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
	" ": 0x20, "space": 0x20,
	"return": 0xff0d, "enter": 0xff0d,
	"tab":    0xff09,
	"escape": 0xff1b, "esc": 0xff1b,
	"up": 0xff52, "down": 0xff54, "left": 0xff51, "right": 0xff53,
	"ctrl": 0xffe3, "shift": 0xffe1, "alt": 0xffe9, "super": 0xffeb,
	"f1": 0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1,
	"f5": 0xffc2, "f6": 0xffc3, "f7": 0xffc4, "f8": 0xffc5,
	"f9": 0xffc6, "f10": 0xffc7, "f11": 0xffc8, "f12": 0xffc9,
}

// keycodeAndLevel looks up the keycode and level for a keysym by searching
// the X server's full GetKeyboardMapping reply. Level 0 means no Shift
// needed; level >= 1 means Shift (or other group) is required. This lets
// the X server's actual keymap dictate which keysyms need Shift rather than
// assuming a US QWERTY layout.
func (b *XTestBackend) keycodeAndLevel(sym xproto.Keysym) (xproto.Keycode, int, error) {
	mapping, err := b.ensureKeymap()
	if err != nil {
		return 0, 0, err
	}
	kl, ok := mapping[sym]
	if !ok {
		return 0, 0, fmt.Errorf("input/xtest: keysym 0x%x not found in keymap", sym)
	}
	return kl.keycode, kl.level, nil
}

func (b *XTestBackend) ensureKeymap() (map[xproto.Keysym]keycodeLevel, error) {
	b.keymapOnce.Do(func() {
		setup := b.conn.Setup()
		first := setup.MinKeycode
		count := byte(setup.MaxKeycode - setup.MinKeycode + 1)
		km, err := b.conn.GetKeyboardMapping(first, count).Reply()
		if err != nil {
			b.keymapErr = fmt.Errorf("input/xtest: GetKeyboardMapping: %w", err)
			return
		}
		kpk := int(km.KeysymsPerKeycode)
		if kpk <= 0 {
			b.keymapErr = fmt.Errorf("input/xtest: invalid keyboard mapping: keysyms_per_keycode=%d", kpk)
			return
		}
		min := int(setup.MinKeycode)
		m := make(map[xproto.Keysym]keycodeLevel, len(km.Keysyms))
		for i, s := range km.Keysyms {
			if s == 0 {
				continue
			}
			if _, exists := m[s]; exists {
				continue
			}
			m[s] = keycodeLevel{
				keycode: xproto.Keycode(min + i/kpk),
				level:   i % kpk,
			}
		}
		b.keymap = m
	})
	return b.keymap, b.keymapErr
}

func (b *XTestBackend) keycodeFor(key string) (xproto.Keycode, error) {
	sym, ok := keysymForName[key]
	if !ok && len(key) == 1 {
		// For single printable ASCII characters not in the map, use the
		// character code directly. Reject control characters and non-ASCII
		// bytes that are not valid keysyms.
		c := key[0]
		if c >= 0x20 && c < 0x7f {
			sym = xproto.Keysym(c)
			ok = true
		}
	}
	if !ok {
		return 0, fmt.Errorf("input/xtest: unknown key %q", key)
	}
	kc, _, err := b.keycodeAndLevel(sym)
	return kc, err
}

// KeyDown presses and holds key through XTEST.
func (b *XTestBackend) KeyDown(ctx context.Context, key string) error {
	return b.withOperation(ctx, func(context.Context) error {
		kc, err := b.keycodeFor(key)
		if err != nil {
			return err
		}
		return b.conn.FakeInputChecked(xproto.KeyPress, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
	})
}

// KeyUp releases a previously held key.
func (b *XTestBackend) KeyUp(ctx context.Context, key string) error {
	return b.withOperation(ctx, func(context.Context) error {
		kc, err := b.keycodeFor(key)
		if err != nil {
			return err
		}
		return b.conn.FakeInputChecked(xproto.KeyRelease, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
	})
}

// Type sends text through XTEST using the input key syntax.
func (b *XTestBackend) Type(ctx context.Context, s string) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.typeContext(ctx, s)
	})
}

// TypeLiteral sends text literally, without interpreting key syntax.
func (b *XTestBackend) TypeLiteral(ctx context.Context, s string) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.typeText(ctx, s)
	})
}

func (b *XTestBackend) typeContext(ctx context.Context, s string) error { //nolint:gocyclo
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
		if err := b.typeAction(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

func (b *XTestBackend) typeAction(ctx context.Context, a keySend) error { //nolint:gocyclo // modifier and key state transitions are intentionally explicit
	if a.text != "" {
		return b.typeText(ctx, a.text)
	}
	if a.key == "" {
		return nil
	}
	kc, err := b.keycodeFor(a.key)
	if err != nil {
		return err
	}
	modKeys, err := b.temporaryModifierKeycodes(a.modifiers)
	if err != nil {
		return err
	}

	pressedMods := make([]xproto.Keycode, 0, len(modKeys))
	cleanupNeeded := true
	defer func() {
		if !cleanupNeeded {
			return
		}
		for i := len(pressedMods) - 1; i >= 0; i-- {
			_ = b.keyUpKC(pressedMods[i])
		}
	}()

	for _, modKey := range modKeys {
		if err := b.keyDownKC(modKey); err != nil {
			return err
		}
		pressedMods = append(pressedMods, modKey)
	}

	switch {
	case a.up:
		if err := b.keyUpKC(kc); err != nil {
			return err
		}
	case a.down:
		if err := b.keyDownKC(kc); err != nil {
			return err
		}
	default:
		if err := b.keyDownKC(kc); err != nil {
			return err
		}
		if err := sleepContext(ctx, b.delay); err != nil {
			if upErr := b.keyUpKC(kc); upErr != nil {
				return upErr
			}
			return err
		}
		if err := b.keyUpKC(kc); err != nil {
			return err
		}
	}

	for i := len(pressedMods) - 1; i >= 0; i-- {
		if err := b.keyUpKC(pressedMods[i]); err != nil {
			return err
		}
		pressedMods = pressedMods[:i]
	}
	cleanupNeeded = false
	return nil
}

func (b *XTestBackend) temporaryModifierKeycodes(mod modifiers) ([]xproto.Keycode, error) {
	keys := make([]string, 0, 4)
	if mod.shift {
		keys = append(keys, "shift")
	}
	if mod.ctrl {
		keys = append(keys, "ctrl")
	}
	if mod.alt {
		keys = append(keys, "alt")
	}
	if mod.super {
		keys = append(keys, "super")
	}

	out := make([]xproto.Keycode, 0, len(keys))
	for _, key := range keys {
		kc, err := b.keycodeFor(key)
		if err != nil {
			return nil, err
		}
		out = append(out, kc)
	}
	return out, nil
}

// typeText types literal text character-by-character using the XTEST keysym
// mapping. Each character's keysym is looked up directly in the server's
// GetKeyboardMapping reply; Shift is held when the keysym lives at level >= 1.
// This is layout-independent: the X server tells us which keysyms need Shift.
func (b *XTestBackend) typeText(ctx context.Context, s string) error {
	ctx = contextutil.Default(ctx)
	for _, ch := range s {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.typeTextRune(ctx, ch); err != nil {
			return err
		}
	}
	return nil
}

func (b *XTestBackend) typeTextRune(ctx context.Context, ch rune) error {
	kc, level, err := b.keycodeAndLevel(xproto.Keysym(ch))
	if err != nil {
		return fmt.Errorf("input/xtest: typeText character %q: %w", string(ch), err)
	}
	shiftHeld := false
	var shiftKC xproto.Keycode
	if level >= 1 {
		shiftKC, err = b.keycodeFor("shift")
		if err != nil {
			return err
		}
		if err := b.keyDownKC(shiftKC); err != nil {
			return err
		}
		shiftHeld = true
	}
	defer func() {
		if shiftHeld {
			_ = b.keyUpKC(shiftKC)
		}
	}()

	if err := b.keyDownKC(kc); err != nil {
		return err
	}
	if err := sleepContext(ctx, b.delay); err != nil {
		if upErr := b.keyUpKC(kc); upErr != nil {
			return upErr
		}
		return err
	}
	if err := b.keyUpKC(kc); err != nil {
		return err
	}
	if shiftHeld {
		if err := b.keyUpKC(shiftKC); err != nil {
			return err
		}
		shiftHeld = false
	}
	return nil
}

func (b *XTestBackend) keyDownKC(kc xproto.Keycode) error {
	return b.conn.FakeInputChecked(xproto.KeyPress, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

func (b *XTestBackend) keyUpKC(kc xproto.Keycode) error {
	return b.conn.FakeInputChecked(xproto.KeyRelease, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

// MouseMove moves the pointer to absolute coordinates x and y.
func (b *XTestBackend) MouseMove(ctx context.Context, x, y int) error {
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseMove(ctx, x, y)
	})
}

func (b *XTestBackend) mouseMove(ctx context.Context, x, y int) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.conn.FakeInputChecked(xproto.MotionNotify, 0,
		xproto.TimeCurrentTime, b.root, int16(x), int16(y), 0).Check()
}

// MouseClick moves to x and y and clicks button.
func (b *XTestBackend) MouseClick(ctx context.Context, x, y, button int) error {
	if err := validateMouseButton("input/xtest", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseClick(ctx, x, y, button)
	})
}

func (b *XTestBackend) mouseClick(ctx context.Context, x, y, button int) error {
	if err := b.mouseMove(ctx, x, y); err != nil {
		return err
	}
	if err := b.mouseButton(ctx, xproto.ButtonPress, button); err != nil {
		return err
	}
	if err := sleepContext(ctx, b.delay); err != nil {
		if upErr := b.mouseButton(context.Background(), xproto.ButtonRelease, button); upErr != nil { //nolint:contextcheck // intentional: release button even if context cancelled
			return upErr
		}
		return err
	}
	return b.mouseButton(context.Background(), xproto.ButtonRelease, button) //nolint:contextcheck // intentional: release button even if context cancelled
}

// MouseDown presses button.
func (b *XTestBackend) MouseDown(ctx context.Context, button int) error {
	if err := validateMouseButton("input/xtest", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseButton(ctx, xproto.ButtonPress, button)
	})
}

func (b *XTestBackend) mouseButton(ctx context.Context, eventType byte, button int) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.conn.FakeInputChecked(eventType, byte(button),
		xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

// MouseUp releases button.
func (b *XTestBackend) MouseUp(ctx context.Context, button int) error {
	if err := validateMouseButton("input/xtest", button); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.mouseButton(ctx, xproto.ButtonRelease, button)
	})
}

// ScrollUp scrolls the mouse wheel up by the given number of notches.
// X11 scroll is button 4 (up) / 5 (down).
func (b *XTestBackend) ScrollUp(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, 4, clicks)
	})
}

// ScrollDown scrolls the mouse wheel down by the given number of notches.
func (b *XTestBackend) ScrollDown(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, 5, clicks)
	})
}

// ScrollLeft scrolls the mouse wheel left by the given number of notches.
// X11 scroll is button 6 (left) / 7 (right).
func (b *XTestBackend) ScrollLeft(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, 6, clicks)
	})
}

// ScrollRight scrolls the mouse wheel right by the given number of notches.
func (b *XTestBackend) ScrollRight(ctx context.Context, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	return b.withOperation(ctx, func(ctx context.Context) error {
		return b.scroll(ctx, 7, clicks)
	})
}

func (b *XTestBackend) scroll(ctx context.Context, button, clicks int) error {
	for i := 0; i < clicks; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.mouseButton(ctx, xproto.ButtonPress, button); err != nil {
			return err
		}
		if err := b.mouseButton(ctx, xproto.ButtonRelease, button); err != nil {
			return err
		}
	}
	return nil
}

// PointerLocation returns the pointer coordinates from XTEST.
func (b *XTestBackend) PointerLocation(ctx context.Context) (int, int, error) {
	var x, y int
	err := b.withOperation(ctx, func(context.Context) error {
		rep, err := b.conn.QueryPointer(b.root).Reply()
		if err != nil {
			return fmt.Errorf("input/xtest: query pointer: %w", err)
		}
		x, y = int(rep.RootX), int(rep.RootY)
		return nil
	})
	return x, y, err
}

// Sync flushes pending XTEST events.
func (b *XTestBackend) Sync(ctx context.Context) error {
	return b.withOperation(ctx, func(context.Context) error {
		b.conn.Sync()
		return nil
	})
}

// Close releases the X11 connection.
func (b *XTestBackend) Close() error {
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
	if b.conn != nil {
		b.conn.Close()
	}

	b.lifecycleMu.Lock()
	b.closeErr = nil
	close(done)
	b.lifecycleMu.Unlock()
	return nil
}
