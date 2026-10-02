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

	// heldMods records which modifier keys the caller is holding through
	// KeyDown. A combination presses the modifiers it needs and releases them
	// afterwards, so one the caller already holds must not be released or the
	// caller's gesture ends partway through.
	heldMu       sync.Mutex
	heldMods     map[xproto.Keycode]bool
	modifierOnce sync.Once
	modCodes     map[xproto.Keycode]bool
	keymapErr    error
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
	// XTest's initial event delay is protocol pacing. Each operation still
	// receives the caller/session context from the bundle.
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
// Letters map to their correct keysyms (A→0x41, a→0x61). typeText queries
// the server keymap to determine which level each keysym lives at, and holds
// the modifiers that level requires; see levelModifiers.
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

// isoLevel3ShiftKeysym is ISO_Level3_Shift, the Mode_switch keysym. Layouts
// that place characters behind AltGr map it to a physical key, usually the
// right Alt.
const isoLevel3ShiftKeysym xproto.Keysym = 0xfe03

// levelModifierCapacity is the number of modifiers a keysym level can require:
// Shift plus the layout's Mode_switch key.
const levelModifierCapacity = 2

// levelModifiers returns the keycodes that must be held to produce a keysym
// found at the given level of the keyboard mapping.
//
// GetKeyboardMapping lists each keycode's levels in a fixed order: unshifted,
// shifted, AltGr, then AltGr+Shift. Only the second level is Shift. Treating
// every level above the first as Shift produced a different character on any
// layout with a third level, so a Euro sign typed on a German layout sent
// Shift+key and delivered whatever Shift yields instead. XTEST can only send
// keycodes, so AltGr has to be applied by pressing the key the layout maps to
// Mode_switch.
func (b *XTestBackend) levelModifiers(level int) ([]xproto.Keycode, error) {
	return b.levelModifiersInto(nil, level)
}

// levelModifiersInto appends levelModifiers' result to dst so a caller typing a
// whole string can reuse one buffer. dst must have room for
// levelModifierCapacity entries; a short one simply grows.
func (b *XTestBackend) levelModifiersInto(dst []xproto.Keycode, level int) ([]xproto.Keycode, error) {
	if level == 0 {
		return dst, nil
	}
	var shift, altGr bool
	switch level {
	case 1:
		shift = true
	case 2:
		altGr = true
	case 3:
		shift, altGr = true, true
	default:
		return dst, fmt.Errorf("input/xtest: keysym level %d exceeds the four levels X11 defines", level)
	}
	if shift {
		kc, err := b.keycodeFor("shift")
		if err != nil {
			return dst, err
		}
		dst = append(dst, kc)
	}
	if altGr {
		kc, err := b.altGrKeycode()
		if err != nil {
			return dst, err
		}
		dst = append(dst, kc)
	}
	return dst, nil
}

// altGrKeycode returns the keycode this layout assigns to Mode_switch, or an
// error when the layout has none, in which case a level-three keysym cannot be
// typed and saying so is better than sending the wrong character.
func (b *XTestBackend) altGrKeycode() (xproto.Keycode, error) {
	mapping, err := b.ensureKeymap()
	if err != nil {
		return 0, err
	}
	kl, ok := mapping[isoLevel3ShiftKeysym]
	if !ok {
		return 0, fmt.Errorf("input/xtest: layout has no Mode_shift key, so keysyms behind AltGr cannot be typed")
	}
	return kl.keycode, nil
}

// keycodeAndLevel looks up the keycode and level for a keysym by searching
// the X server's full GetKeyboardMapping reply. The level selects which
// modifiers must be held; see levelModifiers. This lets the X server's actual
// keymap dictate reachability rather than assuming a US QWERTY layout.
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
		if err := b.conn.FakeInputChecked(xproto.KeyPress, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check(); err != nil {
			return err
		}
		b.markModifierHeld(kc)
		return nil
	})
}

// KeyUp releases a previously held key.
func (b *XTestBackend) KeyUp(ctx context.Context, key string) error {
	return b.withOperation(ctx, func(context.Context) error {
		kc, err := b.keycodeFor(key)
		if err != nil {
			return err
		}
		if err := b.conn.FakeInputChecked(xproto.KeyRelease, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check(); err != nil {
			return err
		}
		b.markModifierReleased(kc)
		return nil
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

func (b *XTestBackend) typeAction(ctx context.Context, a keySend) (err error) { //nolint:gocyclo // modifier and key state transitions are intentionally explicit
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
	// Only the modifiers the caller is not already holding are pressed here, and
	// only those are released afterwards.
	modKeys = b.temporaryModifiers(modKeys)

	pressedMods := make([]xproto.Keycode, 0, len(modKeys))
	cleanupNeeded := true
	defer func() {
		if !cleanupNeeded {
			return
		}
		for i := len(pressedMods) - 1; i >= 0; i-- {
			err = errors.Join(err, b.keyUpKC(pressedMods[i]))
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

// modifierCodeSet resolves the keycodes a combination can request for its
// modifiers, so a held modifier is recognised by keycode regardless of which
// name the caller pressed. The layout's Mode_switch key is included because
// levelModifiers applies it to reach keysyms behind AltGr, and an applied
// modifier this set did not know about would be pressed again on every
// character instead of being recognised as already held.
func (b *XTestBackend) modifierCodeSet() map[xproto.Keycode]bool {
	b.modifierOnce.Do(func() {
		names := modifierNames(modifiers{shift: true, ctrl: true, alt: true, super: true})
		set := make(map[xproto.Keycode]bool, len(names)+1)
		for _, name := range names {
			if kc, err := b.keycodeFor(name); err == nil {
				set[kc] = true
			}
		}
		if kc, err := b.altGrKeycode(); err == nil {
			set[kc] = true
		}
		b.modCodes = set
	})
	return b.modCodes
}

// modifierHeld reports whether the caller is already holding this modifier.
func (b *XTestBackend) modifierHeld(kc xproto.Keycode) bool {
	if !b.modifierCodeSet()[kc] {
		return false
	}
	b.heldMu.Lock()
	defer b.heldMu.Unlock()
	return b.heldMods[kc]
}

func (b *XTestBackend) markModifierHeld(kc xproto.Keycode) {
	if !b.modifierCodeSet()[kc] {
		return
	}
	b.heldMu.Lock()
	defer b.heldMu.Unlock()
	if b.heldMods == nil {
		b.heldMods = make(map[xproto.Keycode]bool, len(b.modCodes))
	}
	b.heldMods[kc] = true
}

func (b *XTestBackend) markModifierReleased(kc xproto.Keycode) {
	b.heldMu.Lock()
	defer b.heldMu.Unlock()
	delete(b.heldMods, kc)
}

// temporaryModifiers returns the modifiers this operation must press, which is
// the requested set minus the ones the caller is already holding.
func (b *XTestBackend) temporaryModifiers(codes []xproto.Keycode) []xproto.Keycode {
	out := make([]xproto.Keycode, 0, len(codes))
	for _, kc := range codes {
		if !b.modifierHeld(kc) {
			out = append(out, kc)
		}
	}
	return out
}

func (b *XTestBackend) temporaryModifierKeycodes(mod modifiers) ([]xproto.Keycode, error) {
	keys := modifierNames(mod)

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
// GetKeyboardMapping reply, and the modifiers that keysym's level requires are
// applied around it. This is layout-independent: the X server tells us which
// keysyms need which modifiers.
func (b *XTestBackend) typeText(ctx context.Context, s string) error {
	ctx = contextutil.Default(ctx)
	// One buffer for the whole string, so a long literal does not allocate a
	// modifier slice per character.
	scratch := make([]xproto.Keycode, 0, levelModifierCapacity)
	for _, ch := range s {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.typeTextRune(ctx, ch, scratch[:0]); err != nil {
			return err
		}
	}
	return nil
}

func (b *XTestBackend) typeTextRune(ctx context.Context, ch rune, scratch []xproto.Keycode) (err error) {
	kc, level, err := b.keycodeAndLevel(xproto.Keysym(ch))
	if err != nil {
		return fmt.Errorf("input/xtest: typeText character %q: %w", string(ch), err)
	}
	required, err := b.levelModifiersInto(scratch, level)
	if err != nil {
		return fmt.Errorf("input/xtest: typeText character %q: %w", string(ch), err)
	}
	// Only the modifiers the caller is not already holding are applied here, and
	// only those are released afterwards.
	pressed := make([]xproto.Keycode, 0, len(required))
	// releaseApplied drains what has been pressed, so calling it from an error
	// path and again from the deferred cleanup cannot release a modifier twice.
	releaseApplied := func(join error) error {
		var cleanupErr error
		for i := len(pressed) - 1; i >= 0; i-- {
			cleanupErr = errors.Join(cleanupErr, b.keyUpKC(pressed[i]))
		}
		pressed = pressed[:0]
		return errors.Join(join, cleanupErr)
	}
	for _, mod := range required {
		if b.modifierHeld(mod) {
			continue
		}
		if keyErr := b.keyDownKC(mod); keyErr != nil {
			return releaseApplied(keyErr)
		}
		pressed = append(pressed, mod)
	}

	defer func() {
		if len(pressed) > 0 {
			err = releaseApplied(err)
		}
	}()
	if err := b.keyDownKC(kc); err != nil {
		return releaseApplied(err)
	}
	if err := sleepContext(ctx, b.delay); err != nil {
		if upErr := b.keyUpKC(kc); upErr != nil {
			return releaseApplied(upErr)
		}
		return releaseApplied(err)
	}
	if err := b.keyUpKC(kc); err != nil {
		return releaseApplied(err)
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
	if x < -1<<15 || x > 1<<15-1 || y < -1<<15 || y > 1<<15-1 {
		return fmt.Errorf("input/xtest: coordinates (%d,%d) exceed X11 int16 range", x, y)
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
