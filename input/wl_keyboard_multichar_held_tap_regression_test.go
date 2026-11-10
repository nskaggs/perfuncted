package input

import (
	"context"
	"strings"
	"testing"
)

// Unknown multi-character key tokens execute as individual taps. A held key
// within that token must be rejected before any keymap or key wire message.
func TestVirtualKeyboardMultiCharacterTokenNeverTapsHeldRune(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"held at end", []keySend{{key: "ba"}}},
		{"held at start", []keySend{{key: "ab"}}},
		{"held after valid prefix", []keySend{{text: "ok"}, {key: "ba"}}},
		{"held earlier in batch", []keySend{{key: "a", down: true}, {key: "ba"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if tc.name != "held earlier in batch" {
				k.held["a"] = kcDynBase
			}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("held rune allowed within multi-character token: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != (map[bool]int{true: 0, false: 1}[tc.name == "held earlier in batch"]) {
				t.Fatalf("invalid batch crossed wire or altered holds: msgs=%d held=%v", len(wire.msgs), k.held)
			}
		})
	}
}
