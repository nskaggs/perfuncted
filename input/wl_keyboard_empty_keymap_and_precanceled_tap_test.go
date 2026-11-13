package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// An empty character set must not synthesize dynamic slots, whose names
// could conflict with modifiers or permit an empty batch to alter the keymap.
func TestVirtualKeyboardEmptyRuneMapUsesOnlyModifierKeymap(t *testing.T) {
	expected := xkbModsOnly()
	actual := xkbWithRunes(nil)
	if actual != expected {
		t.Fatalf("nil rune set introduced dynamic keys into keymap")
	}
	actual = xkbWithRunes([]rune{})
	if actual != expected {
		t.Fatal("empty rune set introduced dynamic keys into keymap")
	}
	if strings.Contains(actual, "<K0") || strings.Contains(actual, "U0000") {
		t.Fatal("empty keymap allocated a dynamic character slot")
	}
}

// An operation canceled before direct tap admission must not send a press or
// rely on cleanup to undo a key event that was never authorized.
func TestVirtualKeyboardDirectTapPreCanceledBeforeWire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k, wire := newTestKeyboard()
	if err := k.tap(ctx, kcDynBase); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled tap returned %v", err)
	}
	if wire.writes != 0 || len(wire.msgs) != 0 {
		t.Fatalf("canceled tap sent %d compositor messages", wire.writes)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("canceled tap mutated input ownership: mods=%d held=%v", k.mods, k.held)
	}
}
