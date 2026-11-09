//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type uncertainTempModifiersWire struct {
	recordingCtx
	pressErr    error
	releaseErr  error
	cancel      context.CancelFunc
	failCleanup bool
}

func (w *uncertainTempModifiersWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if w.writes == 1 {
		if w.cancel != nil {
			w.cancel()
		}
		return w.pressErr
	}
	if w.failCleanup {
		return w.releaseErr
	}
	return nil
}

// A modifier-state write may reach the compositor before the wire reports a
// failure. Restoring only the local mask leaves a stuck Shift in the compositor.
func TestWaylandFailedTemporaryModifierUpdateCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Compares state and error contracts across four wire-failure combinations.
	for _, tc := range []struct {
		name                 string
		held                 uint32
		cancel, cleanupFails bool
	}{
		{name: "no preexisting modifiers"},
		{name: "preserve preexisting Ctrl", held: modControl},
		{name: "cancel after uncertain write", cancel: true},
		{name: "cleanup also fails", held: modControl, cleanupFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("modifier update delivered before transport error")
			releaseErr := errors.New("modifier restore transport error")
			wire := &uncertainTempModifiersWire{pressErr: pressErr, releaseErr: releaseErr, failCleanup: tc.cleanupFails}
			if tc.cancel {
				wire.cancel = cancel
			}
			k, _ := newTestKeyboard()
			k.ctx = wire
			k.mods = tc.held
			applied, err := k.applyTempMods(ctx, modShift)
			if applied != 0 || !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("failed modifier apply=%d error=%v", applied, err)
			}
			if k.mods != tc.held {
				t.Fatalf("local modifier state %d, want %d", k.mods, tc.held)
			}
			if wire.writes != 2 {
				t.Fatalf("uncertain modifier update did not receive compensating wire message: %d", wire.writes)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 2 || events[0].opcode != 2 || events[0].state != tc.held|modShift || events[1].opcode != 2 || events[1].state != tc.held {
				t.Fatalf("modifier wire state not restored: %+v", events)
			}
		})
	}
}

var _ wl.Ctx = (*uncertainTempModifiersWire)(nil)
