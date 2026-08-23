//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type cancelBeforeWriteCtx struct {
	recordingCtx
	cancel       context.CancelFunc
	cancelAt     int
	attempts     int
	cleanupError error
}

func (f *cancelBeforeWriteCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	f.attempts++
	if f.attempts == f.cancelAt {
		f.cancel()
	}
	if f.attempts > f.cancelAt && f.cleanupError != nil {
		return f.cleanupError
	}
	return f.recordingCtx.WriteMsgContext(ctx, data, oob)
}

func TestWlKeyboardTapReleasesAfterCancellationBeforeFinalRelease(t *testing.T) {
	cleanupError := errors.New("keyboard cleanup failed")
	for _, tc := range []struct {
		name         string
		cleanupError error
		wantWrites   int
	}{
		{name: "cleanup succeeds", wantWrites: 2},
		{name: "cleanup fails", cleanupError: cleanupError, wantWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &cancelBeforeWriteCtx{cancel: cancel, cancelAt: 2, cleanupError: tc.cleanupError}
			k := &wlKeyboard{
				ctx:  f,
				kbd:  &wl.RawProxy{},
				held: make(map[string]uint32),
			}
			k.kbd.SetID(42)

			err := k.tap(ctx, 15)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("tap error = %v, want context.Canceled", err)
			}
			if tc.cleanupError != nil && !errors.Is(err, tc.cleanupError) {
				t.Fatalf("tap error = %v, want cleanup error %v joined", err, tc.cleanupError)
			}
			if f.writes != tc.wantWrites {
				t.Fatalf("writes = %d, want %d", f.writes, tc.wantWrites)
			}
			if tc.cleanupError == nil && wl.Uint32(f.msgs[1][16:20]) != 0 {
				t.Fatalf("cleanup key state = %d, want release", wl.Uint32(f.msgs[1][16:20]))
			}
		})
	}
}

func TestWlMouseClickReleasesAfterCancellationBeforeFinalRelease(t *testing.T) {
	cleanupError := errors.New("mouse cleanup failed")
	for _, tc := range []struct {
		name         string
		cleanupError error
		wantWrites   int
		wantJoined   bool
	}{
		{name: "cleanup succeeds", wantWrites: 6},
		{name: "cleanup fails", cleanupError: cleanupError, wantWrites: 4, wantJoined: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &cancelBeforeWriteCtx{cancel: cancel, cancelAt: 5, cleanupError: tc.cleanupError}
			b := &WlVirtualBackend{
				ptr:  &wl.RawProxy{},
				outW: 1920,
				outH: 1080,
			}
			b.ptr.SetID(7)

			err := b.mouseClickEvents(ctx, f, 10, 20, btnLeft)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mouse click error = %v, want context.Canceled", err)
			}
			if tc.wantJoined && !errors.Is(err, tc.cleanupError) {
				t.Fatalf("mouse click error = %v, want cleanup error %v joined", err, tc.cleanupError)
			}
			if f.writes != tc.wantWrites {
				t.Fatalf("writes = %d, want %d", f.writes, tc.wantWrites)
			}
			if tc.cleanupError == nil && wl.Uint32(f.msgs[4][16:20]) != 0 {
				t.Fatalf("cleanup button state = %d, want release", wl.Uint32(f.msgs[4][16:20]))
			}
		})
	}
}
