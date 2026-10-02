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
