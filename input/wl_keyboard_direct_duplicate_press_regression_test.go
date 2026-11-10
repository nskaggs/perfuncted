package input

import (
	"context"
	"strings"
	"testing"
)

// Repeated direct KeyDown must not put a second physical press on the wire.
// In particular, modifiers live in k.mods rather than k.held and previously
// bypassed sendkeys' held-key ownership preflight entirely.
func TestVirtualKeyboardDirectDuplicateKeyDownDoesNotDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		mods      uint32
		held      map[string]uint32
	}{
		{name: "held ctrl", key: "control", mods: modControl},
		{name: "held shift", key: "shift_l", mods: modShift},
		{name: "held named", key: "return", held: map[string]uint32{"enter": kcReturn}},
		{name: "held literal", key: "q", held: map[string]uint32{"q": kcDynBase}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.mods = tc.mods
			for key, slot := range tc.held {
				k.held[key] = slot
			}
			err := k.pressKey(context.Background(), tc.key)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate KeyDown(%q) err=%v; want already held", tc.key, err)
			}
			if len(wire.msgs) != 0 || k.mods != tc.mods || len(k.held) != len(tc.held) {
				t.Fatalf("duplicate press affected state or wire: msgs=%d mods=%d held=%v", len(wire.msgs), k.mods, k.held)
			}
		})
	}
}
