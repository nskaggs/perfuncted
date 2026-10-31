//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A malformed later action must not inject an earlier valid keyboard prefix.
// The complete command is one user operation, not an instruction to type until
// encountering the first provably invalid hold or release.
func TestVirtualKeyboardInvalidSequenceCannotApplyEarlierValidPrefix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial []keySend
		actions []keySend
		message string
	}{
		{"release unheld literal after valid tap", nil, []keySend{{key: "a"}, {key: "b", up: true}}, "not held"},
		{"release unheld named after valid tap", nil, []keySend{{text: "text"}, {key: "enter", up: true}}, "not held"},
		{"release unheld modifier after valid tap", nil, []keySend{{key: "a"}, {key: "ctrl", up: true}}, "not held"},
		{"hold multi-rune after text", nil, []keySend{{text: "hello"}, {key: "bad", down: true}}, "cannot hold"},
		{"double release after valid hold", nil, []keySend{{key: "enter", down: true}, {key: "enter", up: true}, {key: "enter", up: true}}, "not held"},
		{"modifier release double", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl", up: true}, {key: "ctrl", up: true}}, "not held"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.initial) > 0 {
				if err := k.sendkeys(context.Background(), tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			before := len(wire.msgs)
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid keyboard sequence accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid trailing action injected %d premature compositor wire messages: %v", len(wire.msgs)-before, keyboardEventsFromMessages(t, wire.msgs[before:]))
			}
			if k.mods != 0 || len(k.held) != 0 {
				t.Fatalf("invalid sequence changed held state mods=%d held=%v", k.mods, k.held)
			}
		})
	}
}
func TestVirtualKeyboardSequencePreflightHonorsPriorHeldState(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}, {key: "ctrl", down: true}}); err != nil {
		t.Fatal(err)
	}
	before := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", up: true}, {key: "ctrl", up: true}}); err != nil {
		t.Fatalf("valid prior-key release rejected: %v", err)
	}
	if len(wire.msgs) <= before || k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("valid held-key release not sent: mods=%d held=%v messages=%d", k.mods, k.held, len(wire.msgs)-before)
	}
}
