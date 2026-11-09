//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Sequence-level explicit holds must release a possible compositor key-down
// that reported a transport error, without retaining ownership of the hold.
func TestWaylandSequenceFailedExplicitHoldCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three distinct key paths and three failure modes.
	for _, key := range []struct {
		name, value string
		code        uint32
	}{
		{name: "modifier", value: "ctrl", code: kcCtrl},
		{name: "named key", value: "enter", code: 99},
		{name: "literal", value: "é", code: 99},
	} {
		for _, tc := range []struct {
			name                 string
			cancel, cleanupFails bool
		}{
			{name: "release succeeds"},
			{name: "release fails", cleanupFails: true},
			{name: "cancel after uncertain press", cancel: true},
		} {
			t.Run(key.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("press may have reached compositor")
				cleanupErr := errors.New("compensating release failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := k.sendkeys(ctx, []keySend{{key: key.value, down: true}})
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("sequence lost input or cleanup error: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed hold retained local state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != key.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != key.code || events[1].state != 0 {
					t.Fatalf("uncertain sequence hold not balanced: %+v", events)
				}
			})
		}
	}
}
