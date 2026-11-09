//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// An XTEST request can be processed before the X server reports a protocol
// error. When a click's button-down acknowledgement fails, attempt button-up
// instead of leaving the external button state uncertain.
func TestXTestClickCompensatesForUncertainButtonPressFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		releaseFails bool
		cancel       bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
		{name: "canceled during press failure", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("synthetic XTEST button press error")
			releaseErr := errors.New("synthetic XTEST button release error")
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
				if event.eventType == xproto.ButtonPress {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if event.eventType == xproto.ButtonRelease && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			err := b.MouseClick(ctx, 11, 12, 1)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("lost press, release, or cancellation failure: %v", err)
			}
			want := []xtestEvent{
				{eventType: xproto.MotionNotify},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
			}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("button cleanup events=%#v want=%#v", *events, want)
			}
		})
	}
}
