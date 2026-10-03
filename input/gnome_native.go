//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/keymap"
)

var _ Inputter = (*GnomeNativeBackend)(nil)

// gnomeBridge is the GNOME Shell bridge surface this backend uses. Isolating it
// from *gnomebridge.Client lets the key and text paths be exercised without a
// session bus.
type gnomeBridge interface {
	Key(ctx context.Context, keyval uint32, pressed bool) error
	Text(ctx context.Context, text string) error
	Paste(ctx context.Context, text string) error
	PointerMove(ctx context.Context, x, y int32) error
	PointerButton(ctx context.Context, button uint32, pressed bool) error
	PointerLocation(ctx context.Context) (int, int, error)
	Scroll(ctx context.Context, axis string, amount float64) error
	Close() error
}

// GnomeNativeBackend resolves the public input syntax in Go and sends only
// primitive key/pointer notifications through the GNOME bridge.
type GnomeNativeBackend struct {
	bridge gnomeBridge
	// held records which modifiers the caller is holding through KeyDown. A
	// combination presses the modifiers it needs and releases them afterwards,
	// so one the caller already holds must not be released or the caller's
	// gesture ends partway through. It is read and written only inside an
	// operation, which mu serializes.
	held modifiers
	mu   sync.Mutex
}

var gnomeSpecialKeyvals = map[keymap.Key]uint32{
	keymap.KeySpace:     0x20,
	keymap.KeyEnter:     0xff0d,
	keymap.KeyTab:       0xff09,
	keymap.KeyBackspace: 0xff08,
	keymap.KeyEscape:    0xff1b,
	keymap.KeyCtrl:      0xffe3,
	keymap.KeyAlt:       0xffe9,
	keymap.KeyShift:     0xffe1,
	keymap.KeySuper:     0xffeb,
	keymap.KeyUp:        0xff52,
	keymap.KeyDown:      0xff54,
	keymap.KeyLeft:      0xff51,
	keymap.KeyRight:     0xff53,
	keymap.KeyHome:      0xff50,
	keymap.KeyEnd:       0xff57,
	keymap.KeyPageUp:    0xff55,
	keymap.KeyPageDown:  0xff56,
	keymap.KeyInsert:    0xff63,
	keymap.KeyDelete:    0xffff,
}

func gnomeNamedKeyval(k keymap.Key) (uint32, bool) {
	switch {
	case k >= keymap.KeyA && k <= keymap.KeyZ:
		return uint32('a' + int(k-keymap.KeyA)), true
	case k >= keymap.Key0 && k <= keymap.Key9:
		return uint32('0' + int(k-keymap.Key0)), true
	case k >= keymap.KeyF1 && k <= keymap.KeyF12:
		return 0xffbe + uint32(k-keymap.KeyF1), true
	default:
		keyval, ok := gnomeSpecialKeyvals[k]
		return keyval, ok
	}
}

// NewGnomeNativeBackendForRuntime connects to GNOME's bundled virtual-input
// adapter.
func NewGnomeNativeBackendForRuntime(rt env.Runtime) (*GnomeNativeBackend, error) {
	return NewGnomeNativeBackendForRuntimeContext(context.Background(), rt)
}

// NewGnomeNativeBackendForRuntimeContext connects to GNOME's bundled
// virtual-input adapter while honoring ctx during capability negotiation.
func NewGnomeNativeBackendForRuntimeContext(ctx context.Context, rt env.Runtime) (*GnomeNativeBackend, error) {
	ctx = contextutil.Default(ctx)
	bridge, err := gnomebridge.ConnectForCapability(ctx, rt, gnomebridge.CapabilityInput)
	if err != nil {
		return nil, fmt.Errorf("input/gnome-native: %w", err)
	}
	return &GnomeNativeBackend{bridge: bridge}, nil
}

func (b *GnomeNativeBackend) operation(ctx context.Context, fn func(context.Context) error) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("input/gnome-native: backend is not initialised")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bridge == nil {
		return fmt.Errorf("input/gnome-native: backend is not initialised")
	}
	return fn(ctx)
}

func gnomeKeyval(key string) (uint32, error) {
	if k, ok := keymap.FromString(key); ok {
		if keyval, ok := gnomeNamedKeyval(k); ok {
			return keyval, nil
		}
	}
	if len([]rune(key)) == 1 {
		runeValue := []rune(key)[0]
		if runeValue >= 0x20 && runeValue <= 0x10ffff {
			if runeValue < 0x100 {
				return uint32(runeValue), nil
			}
			return uint32(runeValue) | 0x01000000, nil
		}
	}
	return 0, fmt.Errorf("input/gnome-native: unknown key %q", key)
}

// KeyDown presses a named key through GNOME Shell.
func (b *GnomeNativeBackend) KeyDown(ctx context.Context, key string) error {
	keyval, err := gnomeKeyval(key)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error {
		if err := b.bridge.Key(ctx, keyval, true); err != nil {
			return err
		}
		updateHeldModifier(&b.held, key, true)
		return nil
	})
}

// KeyUp releases a named key through GNOME Shell.
func (b *GnomeNativeBackend) KeyUp(ctx context.Context, key string) error {
	keyval, err := gnomeKeyval(key)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error {
		if err := b.bridge.Key(ctx, keyval, false); err != nil {
			return err
		}
		updateHeldModifier(&b.held, key, false)
		return nil
	})
}

// Type sends key syntax through GNOME Shell virtual input.
func (b *GnomeNativeBackend) Type(ctx context.Context, text string) error {
	return b.operation(ctx, func(ctx context.Context) (err error) {
		actions, err := parseKeySend(text)
		if err != nil {
			return err
		}
		// pressedByCall tracks only the modifiers this call brought down, so an
		// error releases those and never a modifier the caller already held.
		pressedByCall := modifiers{}
		defer func() {
			if err != nil && pressedByCall.any() {
				err = errors.Join(err, b.releaseModifierKeys(ctx, gnomeModifierKeyvals(pressedByCall)))
				// The keys are up again, so the bookkeeping has to say so. Leaving a
				// modifier recorded as held made the next call skip pressing it
				// because it believed the key was already down.
				clearHeldModifiers(&b.held, pressedByCall)
				pressedByCall = modifiers{}
			}
		}()
		for _, action := range actions {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if actionErr := b.typeAction(ctx, action, &b.held, &pressedByCall); actionErr != nil {
				return actionErr
			}
		}
		return nil
	})
}

// TypeLiteral sends text without interpreting key syntax. ASCII is sent as
// direct key input; non-ASCII text uses the GNOME clipboard paste fallback and
// consequently updates the clipboard contents.
func (b *GnomeNativeBackend) TypeLiteral(ctx context.Context, text string) error {
	return b.operation(ctx, func(ctx context.Context) error {
		return b.typeText(ctx, text, modifiers{})
	})
}

func (b *GnomeNativeBackend) typeText(ctx context.Context, text string, held modifiers) error {
	if gnomeDirectText(text) {
		return b.bridge.Text(ctx, text)
	}
	if held.any() {
		return fmt.Errorf("input/gnome-native: Unicode text with held modifiers is unavailable: %w", ErrNotSupported)
	}
	return b.bridge.Paste(ctx, text)
}

func gnomeDirectText(text string) bool {
	for _, r := range text {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\b') || r > 0x7e {
			return false
		}
	}
	return true
}

func (m modifiers) any() bool {
	return m.ctrl || m.alt || m.shift || m.super
}

func (b *GnomeNativeBackend) typeAction(
	ctx context.Context,
	action keySend,
	held *modifiers,
	pressedByCall *modifiers,
) error {
	if action.text != "" {
		return b.typeText(ctx, action.text, *held)
	}
	keyval, err := gnomeKeyval(action.key)
	if err != nil {
		return err
	}
	// Only the modifiers the caller is not already holding are pressed here, and
	// only those are released afterwards.
	modifierKeys := b.temporaryModifierKeyvals(action.modifiers, *held)
	pressed := make([]uint32, 0, len(modifierKeys))
	for _, modifierKey := range modifierKeys {
		if keyErr := b.bridge.Key(ctx, modifierKey, true); keyErr != nil {
			return errors.Join(keyErr, b.releaseModifierKeys(ctx, pressed))
		}
		pressed = append(pressed, modifierKey)
	}
	var actionErr error
	switch {
	case action.up:
		actionErr = b.bridge.Key(ctx, keyval, false)
	case action.down:
		actionErr = b.bridge.Key(ctx, keyval, true)
	default:
		if actionErr = b.bridge.Key(ctx, keyval, true); actionErr == nil {
			actionErr = b.bridge.Key(ctx, keyval, false)
		}
	}
	err = errors.Join(actionErr, b.releaseModifierKeys(ctx, pressed))
	if err == nil && (action.down || action.up) {
		updateHeldModifier(held, action.key, action.down)
		updateHeldModifier(pressedByCall, action.key, action.down)
	}
	return err
}

// clearHeldModifiers drops the named modifiers from the held set, for the error
// path where the keys have just been released.
func clearHeldModifiers(held *modifiers, released modifiers) {
	if held == nil {
		return
	}
	if released.ctrl {
		held.ctrl = false
	}
	if released.alt {
		held.alt = false
	}
	if released.shift {
		held.shift = false
	}
	if released.super {
		held.super = false
	}
}

// temporaryModifierKeyvals returns the keyvals for the modifiers a combination
// must press, which is the requested set minus the ones already held.
func (b *GnomeNativeBackend) temporaryModifierKeyvals(mod modifiers, held modifiers) []uint32 {
	requested := modifiers{
		shift: mod.shift && !held.shift,
		ctrl:  mod.ctrl && !held.ctrl,
		alt:   mod.alt && !held.alt,
		super: mod.super && !held.super,
	}
	return gnomeModifierKeyvals(requested)
}

// updateHeldModifier records or clears the modifier a key name resolved to.
//
// The identity comes from the resolved keyval rather than the spelling, because
// the keymap accepts several names for the same modifier: "control_l",
// "shift_l", "alt_l", "super_l" and any casing all resolve to the modifier they
// name. Switching on the raw string missed those, so a modifier held by the
// caller under one spelling was invisible and a later combination released it.
func updateHeldModifier(held *modifiers, key string, down bool) {
	if held == nil {
		return
	}
	for _, bit := range gnomeModifierBits(key) {
		switch bit {
		case modifierCtrl:
			held.ctrl = down
		case modifierAlt:
			held.alt = down
		case modifierShift:
			held.shift = down
		case modifierSuper:
			held.super = down
		}
	}
}

// The modifier identities a key name can resolve to.
const (
	modifierShift = 1 << iota
	modifierCtrl
	modifierAlt
	modifierSuper
)

// gnomeModifierBits reports which modifier a key name resolves to, or none when it
// is not one of the modifiers this backend holds.
func gnomeModifierBits(key string) []uint {
	var bits []uint
	resolved, ok := keymap.FromString(key)
	if !ok {
		return nil
	}
	// FromString canonicalizes every accepted spelling, including the _l forms and
	// any casing, to one key per physical modifier.
	switch resolved {
	case keymap.KeyShift:
		bits = append(bits, modifierShift)
	case keymap.KeyCtrl:
		bits = append(bits, modifierCtrl)
	case keymap.KeyAlt:
		bits = append(bits, modifierAlt)
	case keymap.KeySuper:
		bits = append(bits, modifierSuper)
	}
	return bits
}

func gnomeModifierKeyvals(mod modifiers) []uint32 {
	keys := make([]uint32, 0, 4)
	if mod.shift {
		keys = append(keys, gnomeSpecialKeyvals[keymap.KeyShift])
	}
	if mod.ctrl {
		keys = append(keys, gnomeSpecialKeyvals[keymap.KeyCtrl])
	}
	if mod.alt {
		keys = append(keys, gnomeSpecialKeyvals[keymap.KeyAlt])
	}
	if mod.super {
		keys = append(keys, gnomeSpecialKeyvals[keymap.KeySuper])
	}
	return keys
}

func (b *GnomeNativeBackend) releaseModifierKeys(ctx context.Context, keys []uint32) error {
	if len(keys) == 0 {
		return nil
	}
	// Modifier release is best-effort cleanup after the primary operation.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	var cleanupErr error
	for i := len(keys) - 1; i >= 0; i-- {
		if err := b.bridge.Key(cleanupCtx, keys[i], false); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

// MouseMove moves the pointer to absolute global logical screen coordinates.
func (b *GnomeNativeBackend) MouseMove(ctx context.Context, x, y int) error {
	return b.operation(ctx, func(ctx context.Context) error {
		if err := validateGnomeCoordinates(x, y); err != nil {
			return err
		}
		return b.bridge.PointerMove(ctx, int32(x), int32(y))
	})
}

// PointerCoordinateSpace reports GNOME's global logical screen coordinates.
// Mutter's virtual-input bridge uses the same logical space as GNOME window
// geometry and screenshot requests; browser devicePixelRatio is not involved.
func (b *GnomeNativeBackend) PointerCoordinateSpace(ctx context.Context) (CoordinateSpaceInfo, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return CoordinateSpaceInfo{}, err
	}
	if b == nil || b.bridge == nil {
		return CoordinateSpaceInfo{}, unsupportedError("input/gnome-native", "pointer coordinate space")
	}
	return CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}

func validateGnomeCoordinates(x, y int) error {
	if x < -1<<31 || x > 1<<31-1 || y < -1<<31 || y > 1<<31-1 {
		return fmt.Errorf("input/gnome-native: coordinates (%d,%d) exceed int32 range", x, y)
	}
	return nil
}

func gnomeButton(button int) (uint32, error) {
	if err := validateMouseButton("input/gnome-native", button); err != nil {
		return 0, err
	}
	switch button {
	case 1:
		return 1, nil
	case 2:
		return 2, nil
	default:
		return 3, nil
	}
}

// MouseDown presses a mouse button.
func (b *GnomeNativeBackend) MouseDown(ctx context.Context, button int) error {
	buttonCode, err := gnomeButton(button)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error { return b.bridge.PointerButton(ctx, buttonCode, true) })
}

// MouseUp releases a mouse button.
func (b *GnomeNativeBackend) MouseUp(ctx context.Context, button int) error {
	buttonCode, err := gnomeButton(button)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error { return b.bridge.PointerButton(ctx, buttonCode, false) })
}

// MouseClick moves to a location and clicks a mouse button.
func (b *GnomeNativeBackend) MouseClick(ctx context.Context, x, y, button int) error {
	buttonCode, err := gnomeButton(button)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error {
		if err := validateGnomeCoordinates(x, y); err != nil {
			return err
		}
		if err := b.bridge.PointerMove(ctx, int32(x), int32(y)); err != nil {
			return err
		}
		if err := b.bridge.PointerButton(ctx, buttonCode, true); err != nil {
			return err
		}
		if err := sleepContext(ctx, mouseClickHoldDuration); err != nil {
			return errors.Join(err, releaseMouseButton(ctx, func(cleanupCtx context.Context) error {
				return b.bridge.PointerButton(cleanupCtx, buttonCode, false)
			}))
		}
		return releaseMouseButton(ctx, func(cleanupCtx context.Context) error {
			return b.bridge.PointerButton(cleanupCtx, buttonCode, false)
		})
	})
}

// releaseMouseButton completes a click cleanup even when the operation context
// is canceled. A pressed button must never be left stuck because the caller's
// deadline expired during the hold or immediately before release.
func releaseMouseButton(ctx context.Context, release func(context.Context) error) error {
	ctx = contextutil.Default(ctx)
	// Clipboard/input cleanup must run after cancellation but remains bounded.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	return errors.Join(ctx.Err(), release(cleanupCtx))
}

func (b *GnomeNativeBackend) scroll(ctx context.Context, axis string, clicks int, sign float64) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	if clicks == 0 {
		return nil
	}
	return b.operation(ctx, func(ctx context.Context) error {
		return b.bridge.Scroll(ctx, axis, sign*float64(clicks))
	})
}

// ScrollUp scrolls vertically by the requested number of notches.
func (b *GnomeNativeBackend) ScrollUp(ctx context.Context, clicks int) error {
	return b.scroll(ctx, "vertical", clicks, -1)
}

// ScrollDown scrolls vertically by the requested number of notches.
func (b *GnomeNativeBackend) ScrollDown(ctx context.Context, clicks int) error {
	return b.scroll(ctx, "vertical", clicks, 1)
}

// ScrollLeft scrolls horizontally by the requested number of notches.
func (b *GnomeNativeBackend) ScrollLeft(ctx context.Context, clicks int) error {
	return b.scroll(ctx, "horizontal", clicks, -1)
}

// ScrollRight scrolls horizontally by the requested number of notches.
func (b *GnomeNativeBackend) ScrollRight(ctx context.Context, clicks int) error {
	return b.scroll(ctx, "horizontal", clicks, 1)
}

// PointerLocation returns the current pointer location.
func (b *GnomeNativeBackend) PointerLocation(ctx context.Context) (int, int, error) {
	var x, y int
	err := b.operation(ctx, func(ctx context.Context) error {
		var err error
		x, y, err = b.bridge.PointerLocation(ctx)
		return err
	})
	return x, y, err
}

// Sync is unavailable because Mutter's virtual-input API does not expose a
// completion barrier for its input-thread work.
// Close releases the GNOME bridge connection.
func (b *GnomeNativeBackend) Close() error {
	if b == nil || b.bridge == nil {
		return nil
	}
	return b.bridge.Close()
}
