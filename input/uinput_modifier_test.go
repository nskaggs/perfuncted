package input

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/bendahl/uinput"
)

// countKeyEvents counts "down:<code>" and "up:<code>" entries recorded by
// recordingKeyboard.
func countKeyEvents(events []string, kind string, code int) int {
	prefix := kind + ":" + strconv.Itoa(code)
	n := 0
	for _, e := range events {
		if e == prefix {
			n++
		}
	}
	return n
}

// A modifier the caller is holding through KeyDown must survive a combination
// or literal text that also needs it. Releasing it ends the caller's gesture
// partway, so the characters typed afterwards arrive without it.
func TestUinputCombinationPreservesCallerHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	if err := b.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("Type({ctrl+s}): %v", err)
	}

	downs := countKeyEvents(kb.events, "down", uinput.KeyLeftctrl)
	ups := countKeyEvents(kb.events, "up", uinput.KeyLeftctrl)
	if downs != 1 || ups != 0 {
		t.Fatalf("ctrl key events: %d down, %d up; want 1 down and 0 up so the caller's hold survives\n%v",
			downs, ups, kb.events)
	}
	if !b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("caller's held ctrl was forgotten after a combination that also used it")
	}
}

// A combination must still press and release a modifier the caller is not
// holding, so ordinary typing is unaffected.
func TestUinputCombinationStillReleasesItsOwnModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("Type({ctrl+s}): %v", err)
	}
	downs := countKeyEvents(kb.events, "down", uinput.KeyLeftctrl)
	ups := countKeyEvents(kb.events, "up", uinput.KeyLeftctrl)
	if downs != 1 || ups != 1 {
		t.Fatalf("ctrl key events: %d down, %d up; want exactly one of each\n%v", downs, ups, kb.events)
	}
	if b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a combination left ctrl held")
	}
}

// Literal text that needs shift must not release a shift the caller holds.
func TestUinputLiteralTextPreservesCallerHeldShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "shift"); err != nil {
		t.Fatalf("KeyDown(shift): %v", err)
	}
	if err := b.TypeLiteral(ctx, "ABC"); err != nil {
		t.Fatalf("TypeLiteral(ABC): %v", err)
	}

	if ups := countKeyEvents(kb.events, "up", uinput.KeyLeftshift); ups != 0 {
		t.Fatalf("shift was released %d times while the caller held it\n%v", ups, kb.events)
	}
	if !b.modifierHeld(uinput.KeyLeftshift) {
		t.Fatal("caller's held shift was forgotten after literal text that also needed it")
	}
}

// Literal text still presses and releases its own shift when the caller holds
// none, so uppercase characters continue to work.
func TestUinputLiteralTextStillPressesItsOwnShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.TypeLiteral(ctx, "A"); err != nil {
		t.Fatalf("TypeLiteral(A): %v", err)
	}
	if downs := countKeyEvents(kb.events, "down", uinput.KeyLeftshift); downs != 1 {
		t.Fatalf("shift pressed %d times, want 1\n%v", downs, kb.events)
	}
	if ups := countKeyEvents(kb.events, "up", uinput.KeyLeftshift); ups != 1 {
		t.Fatalf("shift released %d times, want 1\n%v", ups, kb.events)
	}
}

// KeyUp must clear the held state, so a later combination is free to press and
// release the modifier again.
func TestUinputKeyUpClearsHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "alt"); err != nil {
		t.Fatalf("KeyDown(alt): %v", err)
	}
	if err := b.KeyUp(ctx, "alt"); err != nil {
		t.Fatalf("KeyUp(alt): %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftalt) {
		t.Fatal("alt is still held after KeyUp")
	}
	if err := b.Type(ctx, "{alt+f}"); err != nil {
		t.Fatalf("Type({alt+f}): %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftalt) {
		t.Fatalf("a combination left alt held\n%v", kb.events)
	}
}

// Non-modifier keys must not be tracked as modifiers.
func TestUinputNonModifierKeysAreNotTracked(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("KeyDown(a): %v", err)
	}
	for code := range modifierBits {
		if b.modifierHeld(code) {
			t.Fatalf("plain key press marked modifier %d as held", code)
		}
	}
	if err := b.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("KeyUp(a): %v", err)
	}
}

// The modifier set must cover both sides of every modifier with a distinct bit.
func TestModifierBitsCoverLeftAndRightVariants(t *testing.T) {
	for _, code := range []int{
		uinput.KeyLeftshift, uinput.KeyRightshift,
		uinput.KeyLeftctrl, uinput.KeyRightctrl,
		uinput.KeyLeftalt, uinput.KeyRightalt,
		uinput.KeyLeftmeta, uinput.KeyRightmeta,
	} {
		if modifierBit(code) == 0 {
			t.Fatalf("modifier %d has no bit", code)
		}
	}
	if modifierBit(uinput.KeyA) != 0 {
		t.Fatal("a plain key has a modifier bit")
	}
	seen := map[uint32]int{}
	for code, bit := range modifierBits {
		if bit == 0 {
			t.Fatalf("modifier %d maps to an empty bit", code)
		}
		if other, dup := seen[bit]; dup {
			t.Fatalf("modifiers %d and %d share bit %d", other, code, bit)
		}
		seen[bit] = code
	}
	if len(seen) != len(modifierBits) {
		t.Fatalf("modifier table has %d entries but %d distinct bits", len(modifierBits), len(seen))
	}
}

func TestTemporaryModifiersExcludesHeldOnes(t *testing.T) {
	b := &UinputBackend{}
	requested := []int{uinput.KeyLeftshift, uinput.KeyLeftctrl, uinput.KeyLeftalt}

	b.markModifierHeld(uinput.KeyLeftctrl)
	got := b.temporaryModifiers(requested)
	want := []int{uinput.KeyLeftshift, uinput.KeyLeftalt}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("temporaryModifiers = %v, want %v", got, want)
	}

	// Holding none means every requested modifier is this operation's own.
	b2 := &UinputBackend{}
	if got := b2.temporaryModifiers(requested); len(got) != len(requested) {
		t.Fatalf("temporaryModifiers with nothing held = %v, want all %v", got, requested)
	}

	// Holding all means this operation presses nothing.
	for _, mk := range requested {
		b2.markModifierHeld(mk)
	}
	if got := b2.temporaryModifiers(requested); len(got) != 0 {
		t.Fatalf("temporaryModifiers with everything held = %v, want none", got)
	}
}

// A modifier held through the Type down/up syntax has to reach the same
// bookkeeping KeyDown uses. Otherwise it stayed invisible to modifierHeld and the
// next operation pressed it again instead of recognising it was already down.
func TestUinputTypeDownRecordsHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	if err := b.Type(context.Background(), "{ctrl down}"); err != nil {
		t.Fatalf("Type down: %v", err)
	}
	if !b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a modifier held through Type was not recorded as held")
	}
	requested := []int{uinput.KeyLeftctrl, uinput.KeyLeftshift}
	if got := b.temporaryModifiers(requested); len(got) != 1 || got[0] != uinput.KeyLeftshift {
		t.Fatalf("temporaryModifiers = %v, want only the shift; the held ctrl was pressed again", got)
	}

	if err := b.Type(context.Background(), "{ctrl up}"); err != nil {
		t.Fatalf("Type up: %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a modifier released through Type was still recorded as held")
	}
}

// The GNOME backend released the keys it had pressed when a call failed, but left
// the held set saying they were down. The next call then skipped pressing a
// modifier that was no longer down.
func TestGnomeTypeFailureClearsHeldModifiersItReleased(t *testing.T) {
	held := modifiers{ctrl: true}
	clearHeldModifiers(&held, modifiers{ctrl: true})
	if held.any() {
		t.Fatalf("held = %+v, want the released modifier cleared", held)
	}

	// A modifier this call did not press must survive the cleanup.
	held = modifiers{ctrl: true, shift: true}
	clearHeldModifiers(&held, modifiers{ctrl: true})
	if !held.shift || held.ctrl {
		t.Fatalf("held = %+v, want shift kept and ctrl cleared", held)
	}

	var nilHeld *modifiers
	clearHeldModifiers(nilHeld, modifiers{ctrl: true})
}
