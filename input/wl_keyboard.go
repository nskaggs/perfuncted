package input

// wlKeyboard implements zwp_virtual_keyboard_v1 — a pure-Go keyboard that
// uploads a custom XKB keymap for each operation. Each key (Unicode codepoint
// or named key) is assigned its own keycode slot, making this layout-independent.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/keymap"
	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Fixed keycode assignments. XKB keycodes start at 8 (X11 convention).
// Modifier keys occupy 8–11; named keys 12–36; dynamic Unicode starts at 37.
const (
	kcShift   uint32 = 8
	kcCtrl    uint32 = 9
	kcAlt     uint32 = 10
	kcSuper   uint32 = 11
	kcReturn  uint32 = 12
	kcEscape  uint32 = 13
	kcTab     uint32 = 14
	kcBksp    uint32 = 15
	kcDelete  uint32 = 16
	kcHome    uint32 = 17
	kcEnd     uint32 = 18
	kcPgUp    uint32 = 19
	kcPgDown  uint32 = 20
	kcLeft    uint32 = 21
	kcRight   uint32 = 22
	kcUp      uint32 = 23
	kcDown    uint32 = 24
	kcF1      uint32 = 25 // F1–F12: keycodes 25–36
	kcDynBase uint32 = 37 // first dynamic Unicode slot
	kcDynMax  uint32 = 255
)

const maxDynamicKeySlots = kcDynMax - kcDynBase + 1

// XKB modifier bitmask values (standard X11 modifier indices).
const (
	modShift   uint32 = 1
	modControl uint32 = 4
	modMod1    uint32 = 8  // Alt
	modMod4    uint32 = 64 // Super/Meta
)

const xkbFormatTextV1 = 1

type wlKeyboard struct {
	ctx  wl.Ctx
	kbd  *wl.RawProxy
	mods uint32            // currently depressed modifier bitmask
	held map[string]uint32 // non-modifier held keys: canonical name → keycode
	// lastKeymap caches the most recently uploaded keymap text to avoid
	// re-uploading identical keymaps repeatedly.
	// lastMu protects lastKeymap.
	lastMu     sync.Mutex
	lastKeymap string
}

// newWlKeyboard binds zwp_virtual_keyboard_manager_v1 and creates a virtual
// keyboard. seatID is the wl_registry name of the wl_seat (required by protocol).
func newWlKeyboard(cancel context.Context, ctx *wl.Context, registry *wl.Registry, mgrID, mgrVer, seatID uint32) (*wlKeyboard, error) {
	seat := &wl.RawProxy{}
	ctx.Register(seat)
	if err := registry.BindContext(cancel, seatID, "wl_seat", 1, seat.ID()); err != nil {
		return nil, fmt.Errorf("bind wl_seat: %w", err)
	}

	mgr := &wl.RawProxy{}
	ctx.Register(mgr)
	if err := registry.BindContext(cancel, mgrID, "zwp_virtual_keyboard_manager_v1", min(mgrVer, 1), mgr.ID()); err != nil {
		return nil, fmt.Errorf("bind virtual keyboard manager: %w", err)
	}

	k := &wlKeyboard{ctx: ctx, held: make(map[string]uint32)}
	k.kbd = &wl.RawProxy{}
	ctx.Register(k.kbd)

	// create_virtual_keyboard(seat, id): opcode 0, size 16
	var buf [16]byte
	wl.PutUint32(buf[0:], mgr.ID())
	wl.PutUint32(buf[4:], 16<<16) // size=16, opcode=0
	wl.PutUint32(buf[8:], seat.ID())
	wl.PutUint32(buf[12:], k.kbd.ID())
	if err := ctx.WriteMsgContext(cancel, buf[:], nil); err != nil {
		return nil, fmt.Errorf("create virtual keyboard: %w", err)
	}

	// Prime the compositor seat. Some headless compositors drop the first key
	// event if the seat's keyboard state has not been initialised yet.
	if err := k.warmupContext(cancel); err != nil {
		return nil, fmt.Errorf("keyboard warmup: %w", err)
	}
	return k, nil
}

func (k *wlKeyboard) warmupContext(ctx context.Context) error {
	if err := k.uploadKeymap(ctx, xkbModsOnly()); err != nil {
		return err
	}
	if err := k.sendKey(ctx, kcShift, 1); err != nil {
		return err
	}
	// The compositor protocol requires a short modifier settling interval;
	// ctx remains authoritative for cancellation.
	timer := time.NewTimer(2 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return k.sendKey(ctx, kcShift, 0)
}

// uploadKeymapAndRestoreMods uploads a keymap and re-sends the current modifier
// state. Compositors (including sway/wlroots) reset the modifier state when a
// new keymap is installed, so any held modifiers (e.g. Ctrl) must be
// re-declared after every upload.
func (k *wlKeyboard) uploadKeymapAndRestoreMods(ctx context.Context, keymap string) error {
	if err := k.uploadKeymap(ctx, keymap); err != nil {
		return err
	}
	if k.mods != 0 {
		return k.sendModifiers(ctx)
	}
	return nil
}

// applyTempMods adds the modifier bits that are not already held and sends
// the modifier state. It returns only the bits it added so cleanup cannot
// release a modifier that was held before the temporary action started.
func (k *wlKeyboard) applyTempMods(ctx context.Context, modBitmask uint32) (uint32, error) {
	added := modBitmask &^ k.mods
	if added == 0 {
		return 0, nil
	}
	k.mods |= added
	if err := k.sendModifiers(ctx); err != nil {
		k.mods &^= added
		return 0, err
	}
	return added, nil
}

// clearTempMods removes modBitmask from k.mods and sends the update.
func (k *wlKeyboard) clearTempMods(ctx context.Context, modBitmask uint32) error {
	if modBitmask == 0 {
		return nil
	}
	k.mods &^= modBitmask
	return k.sendModifiers(ctx)
}

// clearTempModsBestEffort is like clearTempMods but ignores errors.
// Use on error paths where cleanup must be attempted before returning.
func (k *wlKeyboard) clearTempModsBestEffort(ctx context.Context, modBitmask uint32) {
	if modBitmask == 0 {
		return
	}
	k.mods &^= modBitmask
	// Releasing modifiers is best-effort cleanup after cancellation.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	_ = k.sendModifiers(cleanupCtx)
}

// validateSendkeysSequence simulates held-key transitions without changing
// compositor state or allocating a keymap. Actual transport failures remain
// handled at dispatch time with the existing best-effort key cleanup.
func (k *wlKeyboard) validateSendkeysSequence(actions []keySend) error {
	held := make(map[string]bool, len(k.held))
	for key := range k.held {
		held[key] = true
	}
	mods := k.mods
	for _, action := range actions {
		if action.text != "" {
			continue
		}
		_, _, named := namedKey(action.key)
		bit := uint32(0)
		if named {
			bit = modBit(action.key)
		}
		if action.up {
			if bit != 0 {
				if mods&bit == 0 {
					return fmt.Errorf("keyboard: key %q not held", action.key)
				}
				mods &^= bit
			} else {
				if !held[action.key] {
					return fmt.Errorf("keyboard: key %q not held", action.key)
				}
				delete(held, action.key)
			}
			continue
		}
		if action.down {
			if !named && utf8.RuneCountInString(action.key) != 1 {
				return fmt.Errorf("keyboard: cannot hold multi-character key %q", action.key)
			}
			if bit != 0 {
				mods |= bit
			} else {
				held[action.key] = true
			}
		}
	}
	return nil
}

// sendkeys processes a sequence of parsed keySend actions with a single
// keymap upload. This avoids the modifier-state-reset problem that occurs
// when pressing multiple keys via separate pressKey calls (each uploads a
// new keymap, which resets compositor modifier state).
//
// Text and key actions are executed in the order they appear in the input,
// so "Hello{enter}" types "Hello" before pressing Enter.
func (k *wlKeyboard) sendkeys(ctx context.Context, actions []keySend) error { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if len(actions) == 0 {
		return nil
	}
	// Treat a single typed sequence as one validated command. Without this
	// preflight, an unheld key release or invalid hold at the end of a
	// sequence could inject its earlier valid prefix into the compositor.
	if err := k.validateSendkeysSequence(actions); err != nil {
		return err
	}

	// First pass: collect all unique runes from text actions and all named
	// keycodes so we can build a single keymap that covers everything.
	var allRunes []rune
	seenRunes := make(map[rune]bool)
	type keyAction struct {
		key  string
		down bool
		up   bool
		mod  modifiers
	}
	var keys []keyAction

	for _, a := range actions {
		if a.text != "" {
			for _, r := range a.text {
				if !seenRunes[r] {
					seenRunes[r] = true
					allRunes = append(allRunes, r)
				}
			}
			continue
		}
		keys = append(keys, keyAction{key: a.key, down: a.down, up: a.up, mod: a.modifiers})
		if _, _, ok := namedKey(a.key); ok {
			continue
		}
		for _, r := range a.key {
			if !seenRunes[r] {
				seenRunes[r] = true
				allRunes = append(allRunes, r)
			}
		}
	}
	// A key held by an earlier call must remain mapped to its original
	// scancode until release. Include it even if this command only types
	// unrelated characters; replacing the keymap must not orphan that key.
	heldNames := make([]string, 0, len(k.held))
	for key := range k.held {
		heldNames = append(heldNames, key)
	}
	sort.Strings(heldNames)
	for _, key := range heldNames {
		if _, _, named := namedKey(key); named {
			continue
		}
		r, size := utf8.DecodeRuneInString(key)
		if size != len(key) || r == utf8.RuneError && size == 1 {
			return fmt.Errorf("keyboard: invalid held character %q", key)
		}
		if !seenRunes[r] {
			seenRunes[r] = true
			allRunes = append(allRunes, r)
		}
	}
	// Keep common accelerator keys at stable XKB keycodes across calls.
	// Remapping the same keycode from e.g. 'a' to 'v' between Type("abc")
	// and Type("{ctrl+v}") makes GTK3 miss its own keyboard bindings.
	// Preserve the full dynamic-slot capacity for unusual Unicode-heavy input.
	const acceleratorRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	stableRunes := make([]rune, 0, len(acceleratorRunes)+len(allRunes))
	seenStable := make(map[rune]bool, len(acceleratorRunes)+len(allRunes))
	for _, r := range acceleratorRunes {
		stableRunes = append(stableRunes, r)
		seenStable[r] = true
	}
	for _, r := range allRunes {
		if !seenStable[r] {
			stableRunes = append(stableRunes, r)
			seenStable[r] = true
		}
	}
	// Name order is stable even if the held map is traversed by Go in a
	// different order. All held named keys must be defined in the new map.
	var namedOrder []string
	seenNamed := make(map[string]bool)
	for _, ka := range keys {
		if _, _, named := namedKey(ka.key); named && modBit(ka.key) == 0 && !seenNamed[ka.key] {
			namedOrder = append(namedOrder, ka.key)
			seenNamed[ka.key] = true
		}
	}
	for _, key := range heldNames {
		if _, _, named := namedKey(key); named && modBit(key) == 0 && !seenNamed[key] {
			namedOrder = append(namedOrder, key)
			seenNamed[key] = true
		}
	}
	if len(stableRunes)+len(namedOrder) <= int(maxDynamicKeySlots) {
		allRunes = stableRunes
	}
	if len(allRunes) > int(maxDynamicKeySlots) {
		return fmt.Errorf("keyboard: key sequence has %d unique characters, max %d dynamic slots", len(allRunes), maxDynamicKeySlots)
	}

	// Reserve the original slots before assigning any new characters. In
	// particular a named held key previously at K099 must not be overwritten
	// by the first non-ASCII character in a later batch.
	heldSlots := make(map[uint32]string, len(k.held))
	runeSlots := make(map[rune]uint32, len(allRunes))
	namedSlots := make(map[string]uint32, len(namedOrder))
	for _, key := range heldNames {
		slot := k.held[key]
		if slot < 8 || slot > kcDynMax {
			return fmt.Errorf("keyboard: invalid held slot %d for %q", slot, key)
		}
		if other, occupied := heldSlots[slot]; occupied && other != key {
			return fmt.Errorf("keyboard: held keys %q and %q share slot %d", other, key, slot)
		}
		heldSlots[slot] = key
		if _, _, named := namedKey(key); named {
			namedSlots[key] = slot
		} else {
			r, _ := utf8.DecodeRuneInString(key)
			runeSlots[r] = slot
		}
	}
	nextSlot := kcDynBase
	maxSlot := kcSuper
	takeSlot := func() (uint32, error) {
		for nextSlot <= kcDynMax {
			slot := nextSlot
			nextSlot++
			if _, reserved := heldSlots[slot]; reserved {
				continue
			}
			return slot, nil
		}
		return 0, fmt.Errorf("keyboard: key sequence exceeds %d dynamic slots", maxDynamicKeySlots)
	}
	// Generate XKB definitions only after all allocation succeeds, before
	// touching the compositor or changing persistent held-key bookkeeping.
	var kc, sym strings.Builder
	kc.WriteString(xkbModKeycodes())
	sym.WriteString(xkbModSymbols())
	for _, r := range allRunes {
		slot, ok := runeSlots[r]
		if !ok {
			var err error
			slot, err = takeSlot()
			if err != nil {
				return err
			}
			runeSlots[r] = slot
		}
		if slot > maxSlot {
			maxSlot = slot
		}
		fmt.Fprintf(&kc, "    <K%03d> = %d;\n", slot, slot)
		fmt.Fprintf(&sym, "    key <K%03d> { [ %s ] };\n", slot, xkbKeysym(r))
	}
	for _, key := range namedOrder {
		slot, ok := namedSlots[key]
		if !ok {
			var err error
			slot, err = takeSlot()
			if err != nil {
				return err
			}
			namedSlots[key] = slot
		}
		if slot > maxSlot {
			maxSlot = slot
		}
		_, symbol, _ := namedKey(key)
		fmt.Fprintf(&kc, "    <K%03d> = %d;\n", slot, slot)
		fmt.Fprintf(&sym, "    key <K%03d> { [ %s ] };\n", slot, symbol)
	}

	keymapText := xkbBuild(maxSlot, kc.String(), sym.String())
	if err := k.uploadKeymapAndRestoreMods(ctx, keymapText); err != nil {
		return err
	}

	// Build a lookup from key name to keyAction for the second pass.
	keyIndex := 0

	// Second pass: execute actions in order — text and keys interleaved.
	for _, a := range actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.text != "" {
			// Type literal text.
			for _, r := range a.text {
				slot := runeSlots[r]
				if err := k.tap(ctx, slot); err != nil {
					return err
				}
				// Key sequence pacing is required by the virtual-key protocol.
				if err := sleepContext(ctx, 10*time.Millisecond); err != nil {
					return err
				}
			}
			continue
		}

		// Key action — look it up from the keys slice.
		if keyIndex >= len(keys) {
			continue
		}
		ka := keys[keyIndex]
		keyIndex++

		// Press temporary modifier keys first.
		modBitmask := uint32(0)
		if ka.mod.ctrl {
			modBitmask |= modControl
		}
		if ka.mod.alt {
			modBitmask |= modMod1
		}
		if ka.mod.shift {
			modBitmask |= modShift
		}
		if ka.mod.super {
			modBitmask |= modMod4
		}

		tempMods, err := k.applyTempMods(ctx, modBitmask)
		if err != nil {
			return err
		}

		if kc, _, ok := namedKey(ka.key); ok {
			if bit := modBit(ka.key); bit != 0 {
				// This key is itself a modifier.
				switch {
				case ka.up:
					if k.mods&bit == 0 {
						k.clearTempModsBestEffort(ctx, tempMods)
						return fmt.Errorf("keyboard: key %q not held", ka.key)
					}
					if err := k.sendKey(ctx, kc, 0); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					k.mods &^= bit
					if err := k.sendModifiers(ctx); err != nil {
						// The modifier is still down on the compositor, so the bookkeeping has to keep saying so.
						k.mods |= bit
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
				case ka.down:
					if err := k.sendKey(ctx, kc, 1); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					k.mods |= bit
					if err := k.sendModifiers(ctx); err != nil {
						// The modifier never reached the compositor, so it must not stay recorded as held.
						k.mods &^= bit
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
				default:
					if err := k.sendKey(ctx, kc, 1); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					k.mods |= bit
					if err := k.sendModifiers(ctx); err != nil {
						// The modifier never reached the compositor, so it must not stay recorded as held.
						k.mods &^= bit
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					if err := k.sendKey(ctx, kc, 0); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					k.mods &^= bit
					if err := k.sendModifiers(ctx); err != nil {
						// The modifier is still down on the compositor, so the bookkeeping has to keep saying so.
						k.mods |= bit
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
				}
			} else {
				// Named non-modifier key.
				slot := namedSlots[ka.key]
				switch {
				case ka.up:
					heldSlot, held := k.held[ka.key]
					if !held {
						k.clearTempModsBestEffort(ctx, tempMods)
						return fmt.Errorf("keyboard: key %q not held", ka.key)
					}
					if err := k.sendKey(ctx, heldSlot, 0); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					delete(k.held, ka.key)
				case ka.down:
					if err := k.sendKey(ctx, slot, 1); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					k.held[ka.key] = slot
				default:
					if err := k.sendKey(ctx, slot, 1); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					if err := sleepContext(ctx, 10*time.Millisecond); err != nil {
						// The key-down may already have reached the compositor;
						// cleanup cannot depend on the canceled operation context.
						upErr := k.cleanupSendKey(ctx, slot)
						k.clearTempModsBestEffort(ctx, tempMods)
						return errors.Join(err, upErr)
					}
					if err := k.sendKey(ctx, slot, 0); err != nil {
						upErr := k.cleanupSendKey(ctx, slot)
						k.clearTempModsBestEffort(ctx, tempMods)
						return errors.Join(err, upErr)
					}
				}
			}
		} else {
			// Literal character(s) — tap each rune, or hold/release single-rune keys.
			switch {
			case ka.up:
				heldSlot, held := k.held[ka.key]
				if !held {
					k.clearTempModsBestEffort(ctx, tempMods)
					return fmt.Errorf("keyboard: key %q not held", ka.key)
				}
				if err := k.sendKey(ctx, heldSlot, 0); err != nil {
					k.clearTempModsBestEffort(ctx, tempMods)
					return err
				}
				delete(k.held, ka.key)
			case ka.down:
				runes := []rune(ka.key)
				if len(runes) != 1 {
					k.clearTempModsBestEffort(ctx, tempMods)
					return fmt.Errorf("keyboard: cannot hold multi-character key %q", ka.key)
				}
				slot := runeSlots[runes[0]]
				if err := k.sendKey(ctx, slot, 1); err != nil {
					k.clearTempModsBestEffort(ctx, tempMods)
					return err
				}
				k.held[ka.key] = slot
			default:
				for _, r := range ka.key {
					slot := runeSlots[r]
					if err := k.tap(ctx, slot); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
					if err := sleepContext(ctx, 10*time.Millisecond); err != nil {
						k.clearTempModsBestEffort(ctx, tempMods)
						return err
					}
				}
			}
		}

		// Release temporary modifiers.
		if err := k.clearTempMods(ctx, tempMods); err != nil {
			return err
		}
	}

	return nil
}

// pressKey presses and holds a key. For modifier keys the compositor's modifier
// state is updated via the modifiers request. For other keys the keycode is
// stored for later release via releaseKey.
func (k *wlKeyboard) pressKey(ctx context.Context, key string) error {
	// KeyDown is also a logical sequence. An existing held key reserves its
	// physical XKB slot; uploading a standalone one-key map can silently
	// reinterpret that pressed key as the new one.
	if len(k.held) > 0 {
		return k.sendkeys(ctx, []keySend{{key: key, down: true}})
	}
	if kc, sym, ok := namedKey(key); ok {
		if err := k.uploadKeymapAndRestoreMods(ctx, xkbWithNamed(kc, sym)); err != nil {
			return err
		}
		if err := k.sendKey(ctx, kc, 1); err != nil {
			return err
		}
		if bit := modBit(key); bit != 0 {
			previousMods := k.mods
			k.mods |= bit
			if err := k.sendModifiers(ctx); err != nil {
				k.mods = previousMods
				return errors.Join(err, k.cleanupSendKey(ctx, kc))
			}
			return nil
		}
		k.held[key] = kc
		return nil
	}
	// Single-character non-named key.
	runes := []rune(key)
	if len(runes) != 1 {
		return fmt.Errorf("keyboard: cannot hold multi-character key %q", key)
	}
	if err := k.uploadKeymapAndRestoreMods(ctx, xkbWithRunes(runes)); err != nil {
		return err
	}
	if err := k.sendKey(ctx, kcDynBase, 1); err != nil {
		return err
	}
	k.held[key] = kcDynBase
	return nil
}

// releaseKey releases a previously pressed key. Returns an error if the key
// is not currently held, catching bugs where callers release keys they never
// pressed.
func (k *wlKeyboard) releaseKey(ctx context.Context, key string) error {
	// Modifiers are tracked as a bitset rather than entries in held. Refuse
	// an unmatched release before emitting anything to the compositor.
	if bit := modBit(key); bit != 0 && k.mods&bit == 0 {
		return fmt.Errorf("keyboard: key %q not held", key)
	}
	// KeyUp must use the slot active during KeyDown, not a fresh standalone
	// map that assigns a different code. Restore held modifiers as well.
	if slot, held := k.held[key]; held {
		kc, _, named := namedKey(key)
		if len(k.held) > 1 || k.mods != 0 || (named && slot != kc) || (!named && slot != kcDynBase) {
			return k.sendkeys(ctx, []keySend{{key: key, up: true}})
		}
	}
	if kc, sym, ok := namedKey(key); ok {
		return k.releaseNamedKey(ctx, key, kc, sym)
	}
	// Single-character held key.
	runes := []rune(key)
	if len(runes) != 1 {
		return fmt.Errorf("keyboard: cannot release multi-character key %q", key)
	}
	if _, held := k.held[key]; !held {
		return fmt.Errorf("keyboard: key %q not held", key)
	}
	if err := k.uploadKeymap(ctx, xkbWithRunes(runes)); err != nil {
		return err
	}
	if err := k.sendKey(ctx, kcDynBase, 0); err != nil {
		return err
	}
	delete(k.held, key)
	return nil
}

// releaseNamedKey keeps the wire key-up, modifier mask rollback, and held
// key bookkeeping together for a previously validated named key.
func (k *wlKeyboard) releaseNamedKey(ctx context.Context, key string, kc uint32, sym string) error {
	// Ensure the keymap still defines this keycode (another upload may have
	// replaced it). Modifier keycodes (8–11) are always in every keymap.
	if kc > kcSuper {
		if _, held := k.held[key]; !held {
			return fmt.Errorf("keyboard: key %q not held", key)
		}
		if err := k.uploadKeymap(ctx, xkbWithNamed(kc, sym)); err != nil {
			return err
		}
	}
	if err := k.sendKey(ctx, kc, 0); err != nil {
		return err
	}
	if bit := modBit(key); bit != 0 {
		previousMods := k.mods
		k.mods &^= bit
		if err := k.sendModifiers(ctx); err != nil {
			k.mods = previousMods
			return err
		}
		return nil
	}
	delete(k.held, key)
	return nil
}

// ── Protocol messages ─────────────────────────────────────────────────────────

func (k *wlKeyboard) uploadKeymap(ctx context.Context, text string) error {
	// Hold the lock for the entire upload to prevent concurrent duplicate
	// uploads from multiple goroutines using the same wlKeyboard instance.
	k.lastMu.Lock()
	defer k.lastMu.Unlock()
	if k.lastKeymap == text {
		return nil
	}

	data := text + "\x00"
	f, err := shmutil.CreateFile(int64(len(data)))
	if err != nil {
		return fmt.Errorf("keyboard shm: %w", err)
	}
	if _, err := f.WriteString(data); err != nil {
		return errors.Join(fmt.Errorf("keyboard shm write: %w", err), f.Close())
	}
	var buf [16]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (keymap)
	wl.PutUint32(buf[8:], xkbFormatTextV1)
	wl.PutUint32(buf[12:], uint32(len(data)))
	if err := k.ctx.WriteMsgContext(ctx, buf[:], syscall.UnixRights(int(f.Fd()))); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("keyboard shm close: %w", err)
	}
	k.lastKeymap = text
	return nil
}

func (k *wlKeyboard) sendKey(ctx context.Context, keycode, state uint32) error {
	if keycode < 8 {
		return fmt.Errorf("keyboard: keycode %d is below the XKB base", keycode)
	}
	if state > 1 {
		return fmt.Errorf("keyboard: invalid key state %d", state)
	}
	t := uint32(time.Now().UnixMilli() & 0xffffffff)
	var buf [20]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 20<<16|1) // opcode 1 = key
	wl.PutUint32(buf[8:], t)
	wl.PutUint32(buf[12:], keycode-8) // XKB keycode → evdev scancode
	wl.PutUint32(buf[16:], state)
	return k.ctx.WriteMsgContext(ctx, buf[:], nil)
}

func (k *wlKeyboard) sendModifiers(ctx context.Context) error {
	var buf [24]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 24<<16|2) // opcode 2 = modifiers
	wl.PutUint32(buf[8:], k.mods)   // mods_depressed
	// mods_latched, mods_locked, group all 0
	return k.ctx.WriteMsgContext(ctx, buf[:], nil)
}

func (k *wlKeyboard) tap(ctx context.Context, keycode uint32) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := k.sendKey(ctx, keycode, 1); err != nil {
		return err
	}
	if err := sleepContext(ctx, 10*time.Millisecond); err != nil {
		return errors.Join(err, k.cleanupSendKey(ctx, keycode))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, k.cleanupSendKey(ctx, keycode))
	}
	if err := k.sendKey(ctx, keycode, 0); err != nil {
		return errors.Join(err, k.cleanupSendKey(ctx, keycode))
	}
	return nil
}

// cleanupSendKey releases a key with a bounded independent context. Cleanup
// must still be attempted when the operation context was canceled after the
// press was sent.
func (k *wlKeyboard) cleanupSendKey(ctx context.Context, keycode uint32) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	return k.sendKey(cleanupCtx, keycode, 0)
}

// ── XKB keymap generation ─────────────────────────────────────────────────────

// xkbModsOnly returns a keymap containing only the four modifier keys.
func xkbModsOnly() string {
	return xkbBuild(kcSuper, xkbModKeycodes(), xkbModSymbols())
}

// xkbWithRunes returns a keymap with the modifier block plus one slot per rune.
func xkbWithRunes(runes []rune) string {
	if len(runes) == 0 {
		return xkbModsOnly()
	}
	max := kcDynBase + uint32(len(runes)) - 1
	var kc, sym strings.Builder
	kc.WriteString(xkbModKeycodes())
	sym.WriteString(xkbModSymbols())
	for i, r := range runes {
		slot := kcDynBase + uint32(i)
		fmt.Fprintf(&kc, "    <K%03d> = %d;\n", slot, slot)
		fmt.Fprintf(&sym, "    key <K%03d> { [ %s ] };\n", slot, xkbKeysym(r))
	}
	return xkbBuild(max, kc.String(), sym.String())
}

// xkbWithNamed returns a keymap with the modifier block plus one named key.
// For modifier keycodes (8–11) it returns xkbModsOnly (they are already there).
func xkbWithNamed(kc uint32, sym string) string {
	if kc <= kcSuper {
		return xkbModsOnly()
	}
	keycodes := xkbModKeycodes() + fmt.Sprintf("    <KNAM> = %d;\n", kc)
	symbols := xkbModSymbols() + fmt.Sprintf("    key <KNAM> { [ %s ] };\n", sym)
	return xkbBuild(kc, keycodes, symbols)
}

func xkbBuild(maximum uint32, keycodes, symbols string) string {
	return "xkb_keymap {\n  xkb_keycodes \"perfuncted\" {\n    minimum = 8;\n    maximum = " + strconv.FormatUint(uint64(maximum), 10) + ";\n" + keycodes + "  };\n  xkb_types \"perfuncted\" {\n    type \"ONE_LEVEL\" {\n      modifiers = none;\n      map[none] = Level1;\n      level_name[Level1] = \"Any\";\n    };\n  };\n  xkb_compatibility \"perfuncted\" {\n    interpret Shift_L+AnyOf(all) {\n      action = SetMods(modifiers=Shift);\n    };\n    interpret Control_L+AnyOf(all) {\n      action = SetMods(modifiers=Control);\n    };\n    interpret Alt_L+AnyOf(all) {\n      action = SetMods(modifiers=Mod1);\n    };\n    interpret Super_L+AnyOf(all) {\n      action = SetMods(modifiers=Mod4);\n    };\n  };\n  xkb_symbols \"perfuncted\" {\n" + symbols + "  };\n};"
}

func xkbModKeycodes() string {
	return "    <LFSH> = 8;\n    <LCTL> = 9;\n    <LALT> = 10;\n    <LWIN> = 11;\n"
}

func xkbModSymbols() string {
	return "    key <LFSH> { [ Shift_L ] };\n    key <LCTL> { [ Control_L ] };\n    key <LALT> { [ Alt_L ] };\n    key <LWIN> { [ Super_L ] };\n"
}

// xkbKeysym returns the XKB keysym name for a rune: "U" + uppercase hex codepoint.
func xkbKeysym(r rune) string {
	switch {
	case r >= 'a' && r <= 'z':
		return string(r)
	case r >= 'A' && r <= 'Z':
		return string(r)
	case r >= '0' && r <= '9':
		return string(r)
	case r == ' ':
		return "space"
	}
	return fmt.Sprintf("U%04X", r)
}

// ── Lookup tables ─────────────────────────────────────────────────────────────

// namedKey maps a key name to its fixed keycode and XKB keysym string.
func namedKey(key string) (kc uint32, sym string, ok bool) { //nolint:gocyclo
	if k, found := keymap.FromString(key); found {
		switch k {
		case keymap.KeyShift:
			return kcShift, "Shift_L", true
		case keymap.KeyCtrl:
			return kcCtrl, "Control_L", true
		case keymap.KeyAlt:
			return kcAlt, "Alt_L", true
		case keymap.KeySuper:
			return kcSuper, "Super_L", true
		case keymap.KeyEnter:
			return kcReturn, "Return", true
		case keymap.KeyEscape:
			return kcEscape, "Escape", true
		case keymap.KeyTab:
			return kcTab, "Tab", true
		case keymap.KeyBackspace:
			return kcBksp, "BackSpace", true
		case keymap.KeyDelete:
			return kcDelete, "Delete", true
		case keymap.KeyHome:
			return kcHome, "Home", true
		case keymap.KeyEnd:
			return kcEnd, "End", true
		case keymap.KeyPageUp:
			return kcPgUp, "Prior", true
		case keymap.KeyPageDown:
			return kcPgDown, "Next", true
		case keymap.KeyLeft:
			return kcLeft, "Left", true
		case keymap.KeyRight:
			return kcRight, "Right", true
		case keymap.KeyUp:
			return kcUp, "Up", true
		case keymap.KeyDown:
			return kcDown, "Down", true
		case keymap.KeyF1:
			return kcF1, "F1", true
		case keymap.KeyF2:
			return kcF1 + 1, "F2", true
		case keymap.KeyF3:
			return kcF1 + 2, "F3", true
		case keymap.KeyF4:
			return kcF1 + 3, "F4", true
		case keymap.KeyF5:
			return kcF1 + 4, "F5", true
		case keymap.KeyF6:
			return kcF1 + 5, "F6", true
		case keymap.KeyF7:
			return kcF1 + 6, "F7", true
		case keymap.KeyF8:
			return kcF1 + 7, "F8", true
		case keymap.KeyF9:
			return kcF1 + 8, "F9", true
		case keymap.KeyF10:
			return kcF1 + 9, "F10", true
		case keymap.KeyF11:
			return kcF1 + 10, "F11", true
		case keymap.KeyF12:
			return kcF1 + 11, "F12", true
		}
	}
	return 0, "", false
}

// modBit returns the XKB modifier bitmask for a modifier key name, or 0.
func modBit(key string) uint32 {
	if k, found := keymap.FromString(key); found {
		if keymap.IsModifier(k) {
			switch k {
			case keymap.KeyShift:
				return modShift
			case keymap.KeyCtrl:
				return modControl
			case keymap.KeyAlt:
				return modMod1
			case keymap.KeySuper:
				return modMod4
			}
		}
	}
	return 0
}
