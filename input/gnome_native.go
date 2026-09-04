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

// GnomeNativeBackend resolves the public input syntax in Go and sends only
// primitive key/pointer notifications through the GNOME bridge.
type GnomeNativeBackend struct {
	bridge *gnomebridge.Client
	mu     sync.Mutex
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
	bridge, err := gnomebridge.ConnectForCapability(context.Background(), rt, gnomebridge.CapabilityInput)
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
	return b.operation(ctx, func(ctx context.Context) error { return b.bridge.Key(ctx, keyval, true) })
}

// KeyUp releases a named key through GNOME Shell.
func (b *GnomeNativeBackend) KeyUp(ctx context.Context, key string) error {
	keyval, err := gnomeKeyval(key)
	if err != nil {
		return err
	}
	return b.operation(ctx, func(ctx context.Context) error { return b.bridge.Key(ctx, keyval, false) })
}

// Type sends key syntax through GNOME Shell virtual input.
func (b *GnomeNativeBackend) Type(ctx context.Context, text string) error {
	return b.operation(ctx, func(ctx context.Context) (err error) {
		actions, err := parseKeySend(text)
		if err != nil {
			return err
		}
		held := modifiers{}
		defer func() {
			if err != nil && held.any() {
				err = errors.Join(err, b.releaseModifierKeys(ctx, gnomeModifierKeyvals(held)))
			}
		}()
		for _, action := range actions {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if actionErr := b.typeAction(ctx, action, &held); actionErr != nil {
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

func (b *GnomeNativeBackend) typeAction(ctx context.Context, action keySend, held *modifiers) error {
	if action.text != "" {
		return b.typeText(ctx, action.text, *held)
	}
	keyval, err := gnomeKeyval(action.key)
	if err != nil {
		return err
	}
	modifierKeys := gnomeModifierKeyvals(action.modifiers)
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
	}
	return err
}

func updateHeldModifier(held *modifiers, key string, down bool) {
	if held == nil {
		return
	}
	switch key {
	case "ctrl", "control":
		held.ctrl = down
	case "alt":
		held.alt = down
	case "shift":
		held.shift = down
	case "super", "meta", "win", "logo":
		held.super = down
	}
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
func (b *GnomeNativeBackend) Sync(ctx context.Context) error {
	return b.operation(ctx, func(context.Context) error { return ErrNotSupported })
}

// Close releases the GNOME bridge connection.
func (b *GnomeNativeBackend) Close() error {
	if b == nil || b.bridge == nil {
		return nil
	}
	return b.bridge.Close()
}
