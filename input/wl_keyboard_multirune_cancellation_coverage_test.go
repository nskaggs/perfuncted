//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

func TestWaylandSendkeysCancellationStopsMultiRuneKeyAfterCurrentRune(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newSendkeysTestKeyboard()
	wire := &cancelAfterKeyboardMessage{
		cancel:      cancel,
		matchOpcode: 1,
		matchState:  0,
		matchKey:    true,
	}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{key: "ab"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("multi-rune key cancellation returned %v, want context.Canceled", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].state != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
		t.Fatalf("canceled multi-rune key emitted events %+v, want one balanced rune tap", events)
	}
	if len(wire.msgs) != 3 {
		t.Fatalf("cancellation emitted %d messages, want one keymap and one balanced rune tap", len(wire.msgs))
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("cancellation left keyboard state changed: modifiers=%d held=%v", keyboard.mods, keyboard.held)
	}
}
