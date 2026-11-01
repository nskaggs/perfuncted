//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A Wayland virtual-keyboard tap always contains an up event. It must not
// consume an explicitly held key, including an earlier down in the same batch.
func TestVirtualKeyboardNeverTapsHeldKeyWithoutExplicitRelease(t *testing.T) {
	for _, tc := range []struct {
		name           string
		setup, actions []keySend
	}{
		{"prior held literal", []keySend{{key: "a", down: true}}, []keySend{{key: "a"}}},
		{"prior held named", []keySend{{key: "enter", down: true}}, []keySend{{key: "enter"}}},
		{"prior held modifier", []keySend{{key: "ctrl", down: true}}, []keySend{{key: "ctrl"}}},
		{"same batch literal", nil, []keySend{{key: "a", down: true}, {key: "a"}}},
		{"same batch named after text", nil, []keySend{{text: "private"}, {key: "enter", down: true}, {key: "enter"}}},
		{"same batch modifier", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl"}}},
		{"text taps held letter", []keySend{{key: "a", down: true}}, []keySend{{text: "a"}}},
		{"mixed text taps held letter", []keySend{{key: "a", down: true}}, []keySend{{text: "private a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.setup) > 0 {
				if err := k.sendkeys(context.Background(), tc.setup); err != nil {
					t.Fatalf("establish held key: %v", err)
				}
			}
			before := len(wire.msgs)
			priorMods := k.mods
			priorHeld := make(map[string]uint32, len(k.held))
			for name, slot := range k.held {
				priorHeld[name] = slot
			}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("tap consumed held key without rejecting batch: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid batch transmitted %d compositor messages", len(wire.msgs)-before)
			}
			if k.mods != priorMods || len(k.held) != len(priorHeld) {
				t.Fatalf("rejected batch modified holds: mods=%d expected=%d held=%v expected=%v", k.mods, priorMods, k.held, priorHeld)
			}
			for name, slot := range priorHeld {
				if k.held[name] != slot {
					t.Fatalf("held key %q moved to another slot: before=%d after=%d", name, slot, k.held[name])
				}
			}
		})
	}
}

func TestVirtualKeyboardNeverRepeatsHeldKeyDown(t *testing.T) {
	for _, tc := range []struct {
		name           string
		setup, actions []keySend
	}{
		{"prior held letter", []keySend{{key: "b", down: true}}, []keySend{{key: "b", down: true}}},
		{"prior held modifier", []keySend{{key: "shift", down: true}}, []keySend{{key: "shift", down: true}}},
		{"same batch letter", nil, []keySend{{key: "b", down: true}, {key: "b", down: true}}},
		{"same batch modifier", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl", down: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.setup) > 0 {
				if err := k.sendkeys(context.Background(), tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			before := len(wire.msgs)
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("repeated key down accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("repeated key down transmitted %d wire messages", len(wire.msgs)-before)
			}
		})
	}
}
