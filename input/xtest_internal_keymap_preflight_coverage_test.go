package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// Even when the requested key itself exists, a missing modifier mapping must
// reject direct action dispatch before XTEST can synthesize a key event.
func TestXTestInternalActionRejectsUnmappedModifierBeforeTransport(t *testing.T) {
	b := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{xproto.Keysym('a'): {keycode: 38}}}
	err := b.typeAction(context.Background(), keySend{key: "a", modifiers: modifiers{ctrl: true}})
	if err == nil || !strings.Contains(err.Error(), "not found in keymap") {
		t.Fatalf("unmapped accelerator modifier reached transport: %v", err)
	}
}

// Internal literal dispatch must reject both missing runes and missing level
// modifiers without sending the underlying XTEST keycode to the server.
func TestXTestInternalLiteralRejectsUnavailableRuneAndShiftLevel(t *testing.T) {
	b := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{xproto.Keysym('A'): {keycode: 38, level: 1}}}
	for _, tc := range []struct {
		name string
		ch   rune
	}{
		{"missing rune", 'Ω'},
		{"missing shift modifier", 'A'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.typeTextRune(context.Background(), tc.ch, nil)
			if err == nil || !strings.Contains(err.Error(), "not found in keymap") {
				t.Fatalf("unavailable literal %q reached transport: %v", tc.ch, err)
			}
		})
	}
}
