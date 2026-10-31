//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

func TestVirtualKeyboardDirectLiteralReleaseRetainsHeldSlotAfterWireFailure(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	const key = "é"
	keyboard.held[key] = kcDynBase
	keyboard.lastKeymap = xkbWithRunes([]rune(key))
	peer := &failingCtx{successWrites: 0}
	keyboard.ctx = peer
	err := keyboard.releaseKey(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("failed key-up returned %v", err)
	}
	if slot, ok := keyboard.held[key]; !ok || slot != kcDynBase {
		t.Fatalf("failed key-up dropped tracked key: %#v", keyboard.held)
	}
	if peer.writes != 0 {
		t.Fatalf("failed key-up sent %d successful messages", peer.writes)
	}
	// A recovered compositor channel must still be able to release exactly the
	// originally held slot; losing bookkeeping makes recovery impossible.
	recovered := &recordingCtx{}
	keyboard.ctx = recovered
	if err := keyboard.releaseKey(context.Background(), key); err != nil {
		t.Fatalf("retry release after transport recovery: %v", err)
	}
	events := keyboardEventsFromMessages(t, recovered.msgs)
	if len(events) != 1 || events[0].opcode != 1 || events[0].keycode != kcDynBase || events[0].state != 0 {
		t.Fatalf("recovery did not release the original key: %+v", events)
	}
	if len(keyboard.held) != 0 {
		t.Fatalf("successful retry left held keys: %+v", keyboard.held)
	}
}
