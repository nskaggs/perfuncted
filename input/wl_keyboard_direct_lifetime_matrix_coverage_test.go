//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestVirtualKeyboardDirectKeyLifecycleRejectsInvalidAndUnheldState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*wlKeyboard) error
		want   string
	}{
		{"multi character release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "many") }, "multi-character"},
		{"unheld named release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, "not held"},
		{"unheld character release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "x") }, "not held"},
		{"multi character press", func(k *wlKeyboard) error { return k.pressKey(context.Background(), "multiple") }, "multi-character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := tc.invoke(k)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid key accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid input crossed wire: messages=%d held=%v mods=%d", len(wire.msgs), k.held, k.mods)
			}
		})
	}
}

func TestVirtualKeyboardDirectKeyLifecyclePreservesHeldStateOnWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		preHeld       string
		mods          uint32
		invoke        func(*wlKeyboard) error
		successWrites int
		want          string
	}{
		{"named press keymap", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "enter") }, 0, "write failure"},
		{"named press key", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "enter") }, 1, "write failure"},
		{"literal press keymap", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "é") }, 0, "write failure"},
		{"literal press key", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "é") }, 1, "write failure"},
		{"literal release keymap", "é", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "é") }, 0, "write failure"},
		{"literal release key", "é", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "é") }, 1, "write failure"},
		{"named release keymap", "enter", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, 0, "write failure"},
		{"named release key", "enter", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, 1, "write failure"},
		{"modifier release key", "", modControl, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "ctrl") }, 0, "write failure"},
		{"modifier release mask", "", modControl, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "ctrl") }, 1, "write failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			if tc.preHeld != "" {
				if tc.preHeld == "enter" {
					k.held[tc.preHeld] = kcReturn
				} else {
					k.held[tc.preHeld] = kcDynBase
				}
			}
			k.mods = tc.mods
			peer := &failingCtx{successWrites: tc.successWrites}
			k.ctx = peer
			err := tc.invoke(k)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("write error lost: %v", err)
			}
			if k.mods != tc.mods {
				t.Fatalf("failed write incorrectly changed held modifiers: %d want %d", k.mods, tc.mods)
			}
			if tc.preHeld != "" && len(k.held) != 1 {
				t.Fatalf("failed release untracked key: %v", k.held)
			}
			if tc.preHeld == "" && len(k.held) != 0 {
				t.Fatalf("failed press tracked uncommitted key: %v", k.held)
			}
		})
	}
}

func TestVirtualKeyboardWarmupFailurePreservesCancellationAndTransportError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{"keymap", 0}, {"modifier down", 1}, {"modifier release", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			peer := &failingCtx{successWrites: tc.failAt}
			k.ctx = peer
			err := k.warmupContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "write failure") {
				t.Fatalf("broken warmup accepted: %v", err)
			}
		})
	}
	k, wire := newTestKeyboard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := k.warmupContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled warmup returned %v", err)
	}
	if wire.writes != 0 {
		t.Fatalf("canceled warmup touched compositor %d times", wire.writes)
	}
}
