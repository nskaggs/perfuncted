//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Tap requests that fail after delivery must still balance their key-down
// with an independent release; a failed cleanup must remain observable.
func TestWaylandFailedTapPressCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three distinct tap dispatchers and three transport outcomes.
	for _, method := range []struct {
		name string
		code uint32
		call func(*wlKeyboard, context.Context) error
	}{
		{name: "direct tap", code: kcDynBase, call: func(k *wlKeyboard, ctx context.Context) error { return k.tap(ctx, kcDynBase) }},
		{name: "named sequence tap", code: 99, call: func(k *wlKeyboard, ctx context.Context) error { return k.sendkeys(ctx, []keySend{{key: "enter"}}) }},
		{name: "modifier sequence tap", code: kcCtrl, call: func(k *wlKeyboard, ctx context.Context) error { return k.sendkeys(ctx, []keySend{{key: "ctrl"}}) }},
	} {
		for _, tc := range []struct {
			name                 string
			cancel, cleanupFails bool
		}{
			{name: "release succeeds"},
			{name: "release fails", cleanupFails: true},
			{name: "cancel after uncertain press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XKB tap press reached compositor before transport error")
				cleanupErr := errors.New("XKB tap cleanup failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := method.call(k, ctx)
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("tap lost primary error, cancellation or release error: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed tap retained state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != method.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != method.code || events[1].state != 0 {
					t.Fatalf("failed tap did not balance possible press: %+v", events)
				}
			})
		}
	}
}
