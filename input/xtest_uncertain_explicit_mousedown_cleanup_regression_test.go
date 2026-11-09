//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// An explicit MouseDown can reach the X server before its checked request
// reports an error. A failed hold must attempt a release, not leave a button
// stuck, and must preserve cancellation and any release failure.
func TestXTestMouseDownCompensatesForUncertainPress(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		releaseFails, cancel bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
		{name: "canceled after press", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("XTEST button press acknowledged with error")
			releaseErr := errors.New("XTEST button release failed")
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
				if ev.eventType == xproto.ButtonPress {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if ev.eventType == xproto.ButtonRelease && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			err := b.MouseDown(ctx, 1)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("failed hold did not preserve error(s): %v", err)
			}
			want := []xtestEvent{{eventType: xproto.ButtonPress, detail: 1}, {eventType: xproto.ButtonRelease, detail: 1}}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("failed hold events=%#v, want %#v", *events, want)
			}
		})
	}
}
