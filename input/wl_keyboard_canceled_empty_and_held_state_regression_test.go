//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestVirtualKeyboardCanceledEmptySequenceCannotAcknowledgeSuccess(t *testing.T) {
	keyboard, wire := newTestKeyboard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := keyboard.sendkeys(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-canceled empty key sequence reported success: %v", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("canceled empty sequence wrote to compositor: %v", wire.msgs)
	}
}

func TestVirtualKeyboardRejectsStaleOrCollidingHeldSlotsBeforeCompositorMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		held map[string]uint32
		want string
	}{
		{"below reserved XKB base", map[string]uint32{"a": 7}, "invalid held slot"},
		{"above XKB range", map[string]uint32{"a": kcDynMax + 1}, "invalid held slot"},
		{"two held keys alias same slot", map[string]uint32{"a": kcDynBase, "b": kcDynBase}, "share slot"},
		{"invalid utf8 held key", map[string]uint32{string([]byte{0xff}): kcDynBase}, "invalid held character"},
		{"multi rune held key", map[string]uint32{"ab": kcDynBase}, "invalid held character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.held = tc.held
			if err := k.sendkeys(context.Background(), []keySend{{text: "safe"}}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("stale keyboard state not rejected: %v", err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("stale key state sent compositor frames: %v", wire.msgs)
			}
		})
	}
}
