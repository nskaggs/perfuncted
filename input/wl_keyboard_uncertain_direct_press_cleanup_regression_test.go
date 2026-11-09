//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type uncertainDirectKeyPressWire struct {
	recordingCtx
	pressErr    error
	cleanupErr  error
	cancel      context.CancelFunc
	failCleanup bool
}

func (w *uncertainDirectKeyPressWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 {
		if wl.Uint32(data[16:20]) == 1 {
			if w.cancel != nil {
				w.cancel()
			}
			return w.pressErr
		}
		if w.failCleanup {
			return w.cleanupErr
		}
	}
	return nil
}

// A failed key-down may have reached the compositor; a failed explicit hold
// must release the uncertain key without tracking ownership of the failed hold.
func TestWaylandDirectFailedKeyDownCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three key kinds and three transport outcomes protect independent hold paths.
	for _, key := range []struct {
		name  string
		value string
		code  uint32
	}{
		{name: "modifier", value: "ctrl", code: kcCtrl},
		{name: "named key", value: "enter", code: kcReturn},
		{name: "literal", value: "é", code: kcDynBase},
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
				pressErr := errors.New("press was delivered but reporting failed")
				cleanupErr := errors.New("key release cleanup transport failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := k.pressKey(ctx, key.value)
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press or cleanup errors: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed hold retained local state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != key.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != key.code || events[1].state != 0 {
					t.Fatalf("uncertain hold did not release key: %+v", events)
				}
			})
		}
	}
}
