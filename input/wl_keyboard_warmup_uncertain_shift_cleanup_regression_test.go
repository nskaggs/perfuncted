//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type warmupShiftFailureWire struct {
	recordingCtx
	cancel                               context.CancelFunc
	pressErr, errorRelease, errorCleanup error
	releaseAttempts                      int
}

func (w *warmupShiftFailureWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 {
		state := wl.Uint32(data[16:20])
		if state == 1 {
			if w.cancel != nil {
				w.cancel()
			}
			return w.pressErr
		}
		w.releaseAttempts++
		if w.releaseAttempts == 1 {
			return w.errorRelease
		}
		return w.errorCleanup
	}
	return nil
}

func TestWaylandKeyboardWarmupCompensatesForUncertainShiftState(t *testing.T) { //nolint:gocyclo // Canceled and failed press/release cases use independent wire transitions.
	for _, tc := range []struct {
		name                                                string
		cancelAfterDown, failDown, failRelease, failCleanup bool
	}{
		{name: "canceled after successful down", cancelAfterDown: true},
		{name: "failed down may have reached compositor", failDown: true},
		{name: "canceled failed down", cancelAfterDown: true, failDown: true},
		{name: "release fails once", failRelease: true},
		{name: "both releases fail", failRelease: true, failCleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			downErr := errors.New("warmup shift press was uncertain")
			releaseErr := errors.New("warmup shift release failed")
			cleanupErr := errors.New("warmup shift cleanup failed")
			wire := &warmupShiftFailureWire{}
			if tc.cancelAfterDown {
				wire.cancel = cancel
			}
			if tc.failDown {
				wire.pressErr = downErr
			}
			if tc.failRelease {
				wire.errorRelease = releaseErr
			}
			if tc.failCleanup {
				wire.errorCleanup = cleanupErr
			}
			k, _ := newTestKeyboard()
			k.ctx = wire
			err := k.warmupContext(ctx)
			if tc.cancelAfterDown && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if tc.failDown && !errors.Is(err, downErr) {
				t.Fatalf("lost shift-down error: %v", err)
			}
			if tc.failRelease && !errors.Is(err, releaseErr) {
				t.Fatalf("lost shift-up error: %v", err)
			}
			if tc.failCleanup && !errors.Is(err, cleanupErr) {
				t.Fatalf("lost cleanup error: %v", err)
			}
			wantReleases := 1
			if tc.failRelease {
				wantReleases = 2
			}
			if wire.releaseAttempts != wantReleases {
				t.Fatalf("warmup released Shift %d times, want %d", wire.releaseAttempts, wantReleases)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 1+wantReleases {
				t.Fatalf("warmup did not send shift-down and cleanup: %+v", events)
			}
			for _, ev := range events[1:] {
				if ev.opcode != 1 || ev.keycode != kcShift || ev.state != 0 {
					t.Fatalf("warmup cleanup did not release Shift: %+v", events)
				}
			}
		})
	}
}
