//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// XTEST wheel-button presses can reach X11 before a transport failure is
// reported. No later scrolls are permitted and a possible press must receive
// a compensating release, including after caller cancellation.
func TestXTestScrollCompensatesForUncertainWheelPress(t *testing.T) { //nolint:gocyclo // Four directions and multiple failure modes exercise the same wire contract.
	for _, direction := range []struct {
		name   string
		button byte
		scroll func(*XTestBackend, context.Context, int) error
	}{
		{"up", 4, (*XTestBackend).ScrollUp},
		{"down", 5, (*XTestBackend).ScrollDown},
		{"left", 6, (*XTestBackend).ScrollLeft},
		{"right", 7, (*XTestBackend).ScrollRight},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "release succeeds"},
			{name: "release fails", releaseFails: true},
			{name: "cancel during failed press", cancel: true},
		} {
			t.Run(direction.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XTEST wheel press may have been accepted")
				releaseErr := errors.New("XTEST wheel cleanup failed")
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
					if ev.eventType == xproto.ButtonPress && ev.detail == direction.button {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if ev.eventType == xproto.ButtonRelease && ev.detail == direction.button && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := direction.scroll(b, ctx, 3)
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press, cleanup or cancellation failure: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.ButtonPress, detail: direction.button}, {eventType: xproto.ButtonRelease, detail: direction.button}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("wheel failure injected %#v, want %#v", *events, want)
				}
			})
		}
	}
}
