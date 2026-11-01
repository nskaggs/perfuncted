//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// Inject a transient failed write at every actual request in each of the
// major keyboard forms. A failed tap must leave no depressed key or temporary
// modifiers at either the compositor wire boundary or in the backend's state.
func TestVirtualKeyboardTransientFailureAtEveryWireStepReleasesKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"plain named tap", []keySend{{key: "enter"}}},
		{"literal text", []keySend{{text: "a"}}},
		{"named tap with ctrl", []keySend{{key: "enter", modifiers: modifiers{ctrl: true}}}},
		{"literal key with shift", []keySend{{key: "a", modifiers: modifiers{shift: true}}}},
		{"tap standalone modifier", []keySend{{key: "ctrl"}}},
		{"tap named with all modifiers", []keySend{{key: "f2", modifiers: modifiers{ctrl: true, alt: true, shift: true, super: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reference, _ := newTestKeyboard()
			refWire := &recordingCtx{}
			reference.ctx = refWire
			if err := reference.sendkeys(context.Background(), tc.actions); err != nil {
				t.Fatal(err)
			}
			if refWire.writes < 2 {
				t.Fatalf("test did not exercise a complete key transition: writes=%d", refWire.writes)
			}
			for failAt := 0; failAt < refWire.writes; failAt++ {
				k, _ := newTestKeyboard()
				wire := &failOnceKeyboardWrite{failAt: failAt}
				k.ctx = wire
				err := k.sendkeys(context.Background(), tc.actions)
				if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
					t.Errorf("write %d/%d failure acknowledged: %v", failAt, refWire.writes, err)
					continue
				}
				down := map[uint32]bool{}
				var lastMods uint32
				for _, e := range keyboardEventsFromMessages(t, wire.msgs) {
					switch e.opcode {
					case 1:
						down[e.keycode] = e.state == 1
					case 2:
						lastMods = e.state
					}
				}
				for key, held := range down {
					if held {
						t.Errorf("failure at write %d/%d left keycode %d pressed: %+v", failAt, refWire.writes, key, wire.msgs)
					}
				}
				if lastMods != 0 || k.mods != 0 || len(k.held) != 0 {
					t.Errorf("failure at write %d/%d stranded modifiers: wire=%d mods=%d held=%v", failAt, refWire.writes, lastMods, k.mods, k.held)
				}
			}
		})
	}
}
