//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

func TestWlKeyboardSendkeysFailedModifierReleaseCompensatesAtWire(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     int
		wantKeyUps int
	}{
		{name: "key-up write", failAt: 2, wantKeyUps: 1},
		{name: "modifier state write", failAt: 3, wantKeyUps: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			keyboard.mods = modControl
			wire := &failOnceKeyboardWrite{failAt: tc.failAt}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl", up: true}})
			if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
				t.Fatalf("failed modifier release returned %v", err)
			}
			if keyboard.mods != 0 {
				t.Fatalf("successful compensation retained modifier mask %#x", keyboard.mods)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			keyUps := 0
			modifierStates := make([]uint32, 0, 2)
			for _, event := range events {
				switch event.opcode {
				case 1:
					if event.keycode == kcCtrl && event.state == 0 {
						keyUps++
					}
				case 2:
					modifierStates = append(modifierStates, event.state)
				}
			}
			if keyUps != tc.wantKeyUps {
				t.Fatalf("modifier key-up wire events=%d, want %d: %+v", keyUps, tc.wantKeyUps, events)
			}
			if len(modifierStates) < 2 || modifierStates[0] != modControl || modifierStates[len(modifierStates)-1] != 0 {
				t.Fatalf("modifier state was not restored after failed release: %+v", events)
			}
		})
	}
}

func TestWlKeyboardSendkeysFailedHeldReleaseCanBeRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		slot uint32
	}{
		{name: "named key", key: "enter", slot: kcReturn},
		{name: "literal key", key: "é", slot: kcDynBase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			keyboard.held[tc.key] = tc.slot
			wire := &failingCtx{successWrites: 1}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{{key: tc.key, up: true}})
			if err == nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("failed held-key release returned %v", err)
			}
			if slot, held := keyboard.held[tc.key]; !held || slot != tc.slot {
				t.Fatalf("failed release lost held-key ownership: slot=%d held=%v", slot, held)
			}

			recovery := &recordingCtx{}
			keyboard.ctx = recovery
			if err := keyboard.sendkeys(context.Background(), []keySend{{key: tc.key, up: true}}); err != nil {
				t.Fatalf("retry held-key release: %v", err)
			}
			events := keyboardEventsFromMessages(t, recovery.msgs)
			if len(events) != 1 || events[0].opcode != 1 || events[0].keycode != tc.slot || events[0].state != 0 {
				t.Fatalf("retry did not release original key slot %d: %+v", tc.slot, events)
			}
			if _, held := keyboard.held[tc.key]; held {
				t.Fatalf("successful retry retained held key: %+v", keyboard.held)
			}
		})
	}
}
