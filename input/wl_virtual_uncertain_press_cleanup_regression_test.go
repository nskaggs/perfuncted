//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A failed write may have already reached the compositor. The synthetic
// transport records the delivered frame before returning the write error.
type uncertainWaylandPressCtx struct {
	recordingCtx
	failAt     int
	pressErr   error
	releaseErr error
	attempts   int
	cancel     context.CancelFunc
}

func (f *uncertainWaylandPressCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	f.attempts++
	if f.attempts == f.failAt {
		_ = f.WriteMsg(data, oob)
		if f.cancel != nil {
			f.cancel()
		}
		return f.pressErr
	}
	if f.attempts > f.failAt && f.releaseErr != nil {
		return f.releaseErr
	}
	return f.recordingCtx.WriteMsgContext(ctx, data, oob)
}

func TestWaylandClickCompensatesForUncertainPressWrites(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failAt      int
		cleanupFail bool
		cancel      bool
	}{
		{name: "button event write", failAt: 3},
		{name: "button frame write and cleanup fails", failAt: 4, cleanupFail: true},
		{name: "button event write cancels request", failAt: 3, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("uncertain button-down write")
			releaseErr := errors.New("failed compensating release")
			fake := &uncertainWaylandPressCtx{failAt: tc.failAt, pressErr: pressErr}
			if tc.cleanupFail {
				fake.releaseErr = releaseErr
			}
			if tc.cancel {
				fake.cancel = cancel
			}
			backend := &WlVirtualBackend{ptr: &wl.RawProxy{}, outW: 1920, outH: 1080}
			backend.ptr.SetID(7)
			err := backend.mouseClickEvents(ctx, fake, 10, 20, btnLeft)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.cleanupFail || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("press and cleanup errors not preserved: %v", err)
			}
			expected := tc.failAt
			if !tc.cleanupFail {
				expected += 2
			}
			if fake.writes != expected {
				t.Fatalf("writes=%d, want %d including release frame", fake.writes, expected)
			}
			if !tc.cleanupFail {
				release := fake.msgs[tc.failAt]
				if wl.Uint32(release[4:8])&0xffff != 2 || wl.Uint32(release[16:20]) != 0 {
					t.Fatalf("failed press was not followed by button-up: %x", release)
				}
			}
		})
	}
}
