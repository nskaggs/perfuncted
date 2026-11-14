package input

import (
	"context"
	"errors"
	"testing"
)

// A successful virtual-keyboard press must still be released when the caller
// cancels during the protocol's 10ms key-down pacing delay. The compensating
// release uses a bounded independent context; cleanup errors remain visible.
func TestWaylandTapCancelDuringPacingReleasesPressedKey(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failCleanup bool
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup fails", failCleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cleanupErr := errors.New("synthetic key release transport failure")
			wire := &uncertainDirectKeyPressWire{cancel: cancel, cleanupErr: cleanupErr, failCleanup: tc.failCleanup}
			k, _ := newTestKeyboard()
			k.ctx = wire
			err := k.tap(ctx, kcDynBase)
			if !errors.Is(err, context.Canceled) || errors.Is(err, cleanupErr) != tc.failCleanup {
				t.Fatalf("canceled tap lost cancellation or cleanup failure: %v", err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != kcDynBase || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != kcDynBase || events[1].state != 0 {
				t.Fatalf("cancelled pacing left key held in compositor: %+v", events)
			}
			if k.mods != 0 || len(k.held) != 0 {
				t.Fatalf("cancelled pacing retained ownership: mods=%d held=%v", k.mods, k.held)
			}
		})
	}
}
