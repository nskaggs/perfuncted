package input

// wlKeyboard implements zwp_virtual_keyboard_v1 — a pure-Go keyboard that
// uploads a custom XKB keymap for each operation. Each key (Unicode codepoint
// or named key) is assigned its own keycode slot, making this layout-independent.

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

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

// maxDynSlots is the maximum number of unique Unicode codepoints in one Type call.
const maxDynSlots = kcDynMax - kcDynBase + 1

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
	lastMu     sync.Mutex
	lastKeymap string
}

// newWlKeyboard binds zwp_virtual_keyboard_manager_v1 and creates a virtual
// keyboard. seatID is the wl_registry name of the wl_seat (required by protocol).
func newWlKeyboard(ctx *wl.Context, registry *wl.Registry, mgrID, mgrVer, seatID uint32) (*wlKeyboard, error) {
	seat := &wl.RawProxy{}
	ctx.Register(seat)
	if err := registry.Bind(seatID, "wl_seat", 1, seat.ID()); err != nil {
		return nil, fmt.Errorf("bind wl_seat: %w", err)
	}

	mgr := &wl.RawProxy{}
	ctx.Register(mgr)
	if err := registry.Bind(mgrID, "zwp_virtual_keyboard_manager_v1", min(mgrVer, 1), mgr.ID()); err != nil {
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
	if err := ctx.WriteMsg(buf[:], nil); err != nil {
		return nil, fmt.Errorf("create virtual keyboard: %w", err)
	}

	// Prime the compositor seat. Some headless compositors drop the first key
	// event if the seat's keyboard state has not been initialised yet.
	if err := k.warmup(); err != nil {
		return nil, fmt.Errorf("keyboard warmup: %w", err)
	}
	return k, nil
}

func (k *wlKeyboard) warmup() error {
	if err := k.uploadKeymap(xkbModsOnly()); err != nil {
		return err
	}
	if err := k.sendKey(kcShift, 1); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	return k.sendKey(kcShift, 0)
}

// uploadKeymapAndRestoreMods uploads a keymap and re-sends the current modifier
// state. Compositors (including sway/wlroots) reset the modifier state when a
// new keymap is installed, so any held modifiers (e.g. Ctrl) must be
// re-declared after every upload.
func (k *wlKeyboard) uploadKeymapAndRestoreMods(keymap string) error {
	if err := k.uploadKeymap(keymap); err != nil {
		return err
	}
	if k.mods != 0 {
		return k.sendModifiers()
	}
	return nil
}

// typeString types s by assigning each unique rune its own keycode slot in a
// freshly generated XKB keymap. This is layout-independent: the compositor
// uses our custom keymap, not the system keyboard layout.
func (k *wlKeyboard) typeString(s string) error {
	if s == "" {
		return nil
	}
	runes := uniqueRunes(s)
	if uint32(len(runes)) > maxDynSlots {
		return fmt.Errorf("keyboard: string has %d unique characters, max %d per call", len(runes), maxDynSlots)
	}
	if err := k.uploadKeymapAndRestoreMods(xkbWithRunes(runes)); err != nil {
		return err
	}
	slot := make(map[rune]uint32, len(runes))
	for i, r := range runes {
		slot[r] = kcDynBase + uint32(i)
	}
	for _, r := range s {
		if err := k.tap(slot[r]); err != nil {
			return err
		}
		// Small delay between characters for headless compositors
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// tapKey taps a named key (Return, Escape, Tab, F5, …) or a single character.
// Any modifier state set via pressKey is preserved across the tap.
func (k *wlKeyboard) tapKey(key string) error {
	if kc, sym, ok := namedKey(key); ok {
		if err := k.uploadKeymapAndRestoreMods(xkbWithNamed(kc, sym)); err != nil {
			return err
		}
		return k.tap(kc)
	}
	return k.typeString(key)
}

// pressKey presses and holds a key. For modifier keys the compositor's modifier
// state is updated via the modifiers request. For other keys the keycode is
// stored for later release via releaseKey.
func (k *wlKeyboard) pressKey(key string) error {
	if kc, sym, ok := namedKey(key); ok {
		if err := k.uploadKeymapAndRestoreMods(xkbWithNamed(kc, sym)); err != nil {
			return err
		}
		if err := k.sendKey(kc, 1); err != nil {
			return err
		}
		if bit := modBit(key); bit != 0 {
			k.mods |= bit
			return k.sendModifiers()
		}
		k.held[key] = kc
		return nil
	}
	// Single-character non-named key.
	runes := []rune(key)
	if len(runes) != 1 {
		return fmt.Errorf("keyboard: cannot hold multi-character key %q", key)
	}
	if err := k.uploadKeymapAndRestoreMods(xkbWithRunes(runes)); err != nil {
		return err
	}
	if err := k.sendKey(kcDynBase, 1); err != nil {
		return err
	}
	k.held[key] = kcDynBase
	return nil
}

// releaseKey releases a previously pressed key.
func (k *wlKeyboard) releaseKey(key string) error {
	if kc, sym, ok := namedKey(key); ok {
		// Ensure the keymap still defines this keycode (another upload may have
		// replaced it). Modifier keycodes (8–11) are always in every keymap.
		if kc > kcSuper {
			if err := k.uploadKeymap(xkbWithNamed(kc, sym)); err != nil {
				return err
			}
		}
		if err := k.sendKey(kc, 0); err != nil {
			return err
		}
		if bit := modBit(key); bit != 0 {
			k.mods &^= bit
			return k.sendModifiers()
		}
		delete(k.held, key)
		return nil
	}
	// Single-character held key.
	runes := []rune(key)
	if len(runes) != 1 {
		return nil
	}
	if _, held := k.held[key]; !held {
		return nil
	}
	if err := k.uploadKeymap(xkbWithRunes(runes)); err != nil {
		return err
	}
	delete(k.held, key)
	return k.sendKey(kcDynBase, 0)
}

// ── Protocol messages ─────────────────────────────────────────────────────────

func (k *wlKeyboard) uploadKeymap(text string) error {
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
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		return fmt.Errorf("keyboard shm write: %w", err)
	}
	var buf [16]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (keymap)
	wl.PutUint32(buf[8:], xkbFormatTextV1)
	wl.PutUint32(buf[12:], uint32(len(data)))
	if err := k.ctx.WriteMsg(buf[:], syscall.UnixRights(int(f.Fd()))); err != nil {
		return err
	}
	k.lastKeymap = text
	return nil
}

func (k *wlKeyboard) sendKey(keycode, state uint32) error {
	t := uint32(time.Now().UnixMilli() & 0xffffffff)
	var buf [20]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 20<<16|1) // opcode 1 = key
	wl.PutUint32(buf[8:], t)
	wl.PutUint32(buf[12:], keycode-8) // XKB keycode → evdev scancode
	wl.PutUint32(buf[16:], state)
	return k.ctx.WriteMsg(buf[:], nil)
}

func (k *wlKeyboard) sendModifiers() error {
	var buf [24]byte
	wl.PutUint32(buf[0:], k.kbd.ID())
	wl.PutUint32(buf[4:], 24<<16|2) // opcode 2 = modifiers
	wl.PutUint32(buf[8:], k.mods)   // mods_depressed
	// mods_latched, mods_locked, group all 0
	return k.ctx.WriteMsg(buf[:], nil)
}

func (k *wlKeyboard) tap(keycode uint32) error {
	if err := k.sendKey(keycode, 1); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return k.sendKey(keycode, 0)
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
	return fmt.Sprintf(`xkb_keymap {
  xkb_keycodes "perfuncted" {
    minimum = 8;
    maximum = %d;
%s  };
  xkb_types "perfuncted" {
    type "ONE_LEVEL" {
      modifiers = none;
      map[none] = Level1;
      level_name[Level1] = "Any";
    };
  };
  xkb_compatibility "perfuncted" {
    interpret Shift_L+AnyOf(all) {
      action = SetMods(modifiers=Shift);
    };
    interpret Control_L+AnyOf(all) {
      action = SetMods(modifiers=Control);
    };
    interpret Alt_L+AnyOf(all) {
      action = SetMods(modifiers=Mod1);
    };
    interpret Super_L+AnyOf(all) {
      action = SetMods(modifiers=Mod4);
    };
  };
  xkb_symbols "perfuncted" {
%s  };
};`, maximum, keycodes, symbols)
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
func namedKey(key string) (kc uint32, sym string, ok bool) {
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

// uniqueRunes returns the unique runes in s in first-occurrence order.
func uniqueRunes(s string) []rune {
	seen := make(map[rune]bool)
	var out []rune
	for _, r := range s {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
