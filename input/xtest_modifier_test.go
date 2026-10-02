//go:build linux
// +build linux

package input

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// countXTestEvents counts recorded XTEST events of a kind for a keycode detail.
func countXTestEvents(events []xtestEvent, eventType byte, detail byte) int {
	n := 0
	for _, e := range events {
		if e.eventType == eventType && e.detail == detail {
			n++
		}
	}
	return n
}

// keycodeForCtrl resolves the ctrl keycode the same way the backend does, so
// the assertions do not hardcode a keymap-specific value.
func keycodeForCtrl(t *testing.T, b *XTestBackend) byte {
	t.Helper()
	kc, err := b.keycodeFor("ctrl")
	if err != nil {
		t.Fatalf("keycodeFor(ctrl): %v", err)
	}
	return byte(kc)
}

// newModifierXTestBackend builds a backend over a keymap that resolves every
// modifier plus a plain key, with one keysym per keycode so the keycodes are
// consecutive from MinKeycode.
func newModifierXTestBackend(t *testing.T) (*XTestBackend, *[]xtestEvent) {
	t.Helper()
	return newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, nil)
}

// A modifier the caller is holding through KeyDown must survive a combination
// that also needs it.
func TestXTestCombinationPreservesCallerHeldModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	*events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}

	downs := countXTestEvents(*events, xproto.KeyPress, ctrl)
	ups := countXTestEvents(*events, xproto.KeyRelease, ctrl)
	if downs != 0 || ups != 0 {
		t.Fatalf("ctrl key events during the combination: %d press, %d release; want none so the caller's hold survives\n%v",
			downs, ups, *events)
	}
	if !b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("caller's held ctrl was forgotten after a combination that also used it")
	}
}

// A combination must still press and release a modifier the caller is not
// holding.
func TestXTestCombinationStillReleasesItsOwnModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, ctrl); downs != 1 {
		t.Fatalf("ctrl pressed %d times, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, ctrl); ups != 1 {
		t.Fatalf("ctrl released %d times, want 1\n%v", ups, *events)
	}
	if b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("a combination left ctrl held")
	}
}

// KeyUp clears the held state so a later combination owns the modifier again.
func TestXTestKeyUpClearsHeldModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	if err := b.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyUp(ctrl): %v", err)
	}
	if b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("ctrl is still held after KeyUp")
	}
	*events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, ctrl); downs != 1 {
		t.Fatalf("ctrl pressed %d times after KeyUp, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, ctrl); ups != 1 {
		t.Fatalf("ctrl released %d times after KeyUp, want 1\n%v", ups, *events)
	}
}

// Only modifier keycodes are tracked as held.
func TestXTestNonModifierKeysAreNotTracked(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	ctx := context.Background()

	if err := b.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("KeyDown(a): %v", err)
	}
	for kc := range b.modifierCodeSet() {
		if b.modifierHeld(kc) {
			t.Fatalf("plain key press marked modifier %d as held", kc)
		}
	}
}

// temporaryModifiers drops the ones the caller already holds.
func TestXTestTemporaryModifiersExcludesHeldOnes(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	requested, err := b.temporaryModifierKeycodes(modifiers{shift: true, ctrl: true, alt: true, super: true})
	if err != nil {
		t.Fatalf("temporaryModifierKeycodes: %v", err)
	}
	if len(requested) == 0 {
		t.Fatal("no modifier keycodes resolved")
	}

	if got := b.temporaryModifiers(requested); len(got) != len(requested) {
		t.Fatalf("temporaryModifiers with nothing held = %v, want all %v", got, requested)
	}

	b.markModifierHeld(requested[0])
	got := b.temporaryModifiers(requested)
	if len(got) != len(requested)-1 {
		t.Fatalf("temporaryModifiers = %v, want %v without the held modifier", got, requested)
	}
	for _, kc := range got {
		if kc == requested[0] {
			t.Fatal("temporaryModifiers kept a modifier the caller is holding")
		}
	}
}

// The modifier code set must resolve every modifier a combination can request.
func TestXTestModifierCodeSetCoversAllModifiers(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	set := b.modifierCodeSet()
	for _, name := range modifierNames(modifiers{shift: true, ctrl: true, alt: true, super: true}) {
		kc, err := b.keycodeFor(name)
		if err != nil {
			t.Fatalf("keycodeFor(%q): %v", name, err)
		}
		if !set[kc] {
			t.Fatalf("modifier %q (keycode %d) is missing from the held-modifier set", name, kc)
		}
	}
}

// germanLikeKeysyms is a four-level keymap in the order GetKeyboardMapping
// reports them: unshifted, shifted, AltGr, AltGr+Shift. The first keycode is
// mapped to ISO_Level3_Shift the way a German layout maps the right Alt.
func germanLikeKeysyms() []xproto.Keysym {
	return []xproto.Keysym{
		// ctrl
		0xffe3, 0, 0, 0,
		// shift
		0xffe1, 0, 0, 0,
		// ISO_Level3_Shift, the Mode_switch key
		isoLevel3ShiftKeysym, 0, 0, 0,
		// alt
		0xffe9, 0, 0, 0,
		// e, E, Euro sign, at sign
		0x65, 0x45, 0x20ac, 0x40,
		// s, S, sharp s, question mark
		0x73, 0x53, 0xdf, 0x3f,
	}
}

const keycodesPerKeycode4 = 4

// A keysym at the AltGr level must be produced by pressing the layout's
// Mode_switch key. Pressing Shift, as the level index alone used to imply,
// delivers a different character with no error.
func TestXTestLevelThreeUsesModeSwitchNotShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	altGrCode, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}
	if altGrCode == shiftCode {
		t.Fatalf("altGrKeycode = %d, which is also the shift keycode", altGrCode)
	}

	if err := b.TypeLiteral(ctx, "€"); err != nil {
		t.Fatalf("TypeLiteral(Euro sign): %v", err)
	}

	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(shiftCode)); ups != 0 {
		t.Fatalf("shift released %d times typing an AltGr keysym, want 0\n%v", ups, *events)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(altGrCode)); downs != 1 {
		t.Fatalf("Mode_shift pressed %d times, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(altGrCode)); ups != 1 {
		t.Fatalf("Mode_shift released %d times, want 1\n%v", ups, *events)
	}
}

// The second level is the only shifted one, so an uppercase letter still uses
// Shift and nothing else.
func TestXTestLevelTwoStillUsesShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	altGrCode, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}

	if err := b.TypeLiteral(ctx, "E"); err != nil {
		t.Fatalf("TypeLiteral(E): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(shiftCode)); downs != 1 {
		t.Fatalf("shift pressed %d times, want 1\n%v", downs, *events)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(altGrCode)); downs != 0 {
		t.Fatalf("Mode_shift pressed %d times for a shifted keysym, want 0\n%v", downs, *events)
	}
}

// A layout with no Mode_shift key cannot type a level-three keysym. Reporting
// that is better than sending a key combination that produces another character.
func TestXTestAltGrWithoutModeShiftKeyIsReported(t *testing.T) {
	// Three levels per keycode, so the Euro sign has no Mode_shift to press.
	b, _ := newTestXTestBackend(t, 3, []xproto.Keysym{
		0xffe3, 0xffe1, 0xffe9, // ctrl, shift, alt
		0x65, 0x45, 0x20ac, // e, E, Euro sign
	}, nil)

	if _, err := b.altGrKeycode(); err == nil {
		t.Fatal("altGrKeycode resolved without a Mode_shift key")
	}
	if err := b.TypeLiteral(context.Background(), "€"); err == nil {
		t.Fatal("TypeLiteral typed an AltGr keysym on a layout without Mode_shift")
	}
}

// Level one needs no modifiers at all.
func TestXTestLevelOneNeedsNoModifiers(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	codes, err := b.levelModifiers(0)
	if err != nil {
		t.Fatalf("levelModifiers(0): %v", err)
	}
	if len(codes) != 0 {
		t.Fatalf("level 0 = %v, want no modifiers", codes)
	}
}

// A level beyond the four X11 defines is rejected rather than guessed.
func TestXTestLevelBeyondFourIsRejected(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	if _, err := b.levelModifiers(4); err == nil {
		t.Fatal("level 4 was accepted")
	}
}

// A caller holding shift does not have it pressed or released again for a
// shifted character.
func TestXTestShiftedCharacterPreservesCallerHeldShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	if err := b.KeyDown(ctx, "shift"); err != nil {
		t.Fatalf("KeyDown(shift): %v", err)
	}
	*events = nil
	if err := b.TypeLiteral(ctx, "E"); err != nil {
		t.Fatalf("TypeLiteral(E): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(shiftCode)); downs != 0 {
		t.Fatalf("shift pressed %d times, want 0\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(shiftCode)); ups != 0 {
		t.Fatalf("shift released %d times, want 0\n%v", ups, *events)
	}
	if !b.modifierHeld(shiftCode) {
		t.Fatal("caller's held shift was forgotten")
	}
}

// levelModifiers applies the layout's Mode_switch key to reach keysyms behind
// AltGr. Because modifierCodeSet is what recognises an already-held modifier by
// keycode, the Mode_switch keycode has to be in that set: without it, a caller
// holding AltGr has it pressed again on every character and released again after
// each one, so the caller's own hold is broken by ordinary text.
func TestXTestModifierCodeSetIncludesLayoutModeSwitchKey(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)

	altGr, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}
	if !b.modifierCodeSet()[altGr] {
		t.Fatalf("Mode_switch keycode %d is not in the modifier code set; a caller holding AltGr would have it re-pressed per character", altGr)
	}
	if b.modifierHeld(altGr) {
		t.Fatal("Mode_switch keycode reported as held before anything pressed it")
	}
	b.markModifierHeld(altGr)
	if !b.modifierHeld(altGr) {
		t.Fatal("a Mode_shift key the caller is holding was not recognised")
	}

	// A character behind AltGr must then leave the caller's hold alone.
	required, err := b.levelModifiers(2)
	if err != nil {
		t.Fatalf("levelModifiers(2): %v", err)
	}
	for _, kc := range b.temporaryModifiers(required) {
		if kc == altGr {
			t.Fatal("temporaryModifiers wants to press a Mode_shift key the caller already holds")
		}
	}
}
