//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// Explicit holds have no later tap-release phase. A failed XTEST press may
// already have reached the server, so failed holds must compensate without
// claiming the modifier is owned by the caller.
func TestXTestExplicitKeyDownCompensatesForUncertainPress(t *testing.T) { //nolint:gocyclo // Two entrypoints and two key kinds cover independent hold-ownership transitions.
	for _, tc := range []struct {
		name, key                     string
		useType, releaseFails, cancel bool
	}{
		{name: "KeyDown modifier", key: "ctrl"},
		{name: "KeyDown normal key", key: "a", releaseFails: true},
		{name: "Type explicit modifier down", key: "ctrl", useType: true, cancel: true},
		{name: "Type explicit normal down", key: "a", useType: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("explicit key-down write was uncertain")
			releaseErr := errors.New("compensating key-up failed")
			code := byte(8)
			if tc.key == "a" {
				code = 9
			}
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3, 0x61}, func(ev xtestEvent) error {
				if ev.eventType == xproto.KeyPress && ev.detail == code {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if ev.eventType == xproto.KeyRelease && ev.detail == code && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			var err error
			if tc.useType {
				err = b.Type(ctx, "{"+tc.key+" down}")
			} else {
				err = b.KeyDown(ctx, tc.key)
			}
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("lost press, cleanup or cancellation error: %v", err)
			}
			want := []xtestEvent{{eventType: xproto.KeyPress, detail: code}, {eventType: xproto.KeyRelease, detail: code}}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("failed explicit hold events=%#v, want %#v", *events, want)
			}
			if b.modifierHeld(xproto.Keycode(8)) {
				t.Fatal("failed hold marked Ctrl as caller-owned")
			}
		})
	}
}
