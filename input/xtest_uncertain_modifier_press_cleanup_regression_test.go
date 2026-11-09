//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// When XTEST reports an error on a temporary modifier press, that press may
// already have reached the X server. Compensating release must precede any
// prior modifiers' cleanup, even on request cancellation.
func TestXTestTemporaryModifierCompensatesForUncertainPress(t *testing.T) { //nolint:gocyclo // Six cases verify release order and joined failures across two modifier paths.
	for _, method := range []struct {
		name         string
		kpk          byte
		keysyms      []xproto.Keysym
		key          string
		failCode     byte
		previousCode byte
	}{
		{name: "key syntax ctrl then shift", kpk: 1, keysyms: []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb, 0x61}, key: "{ctrl+shift+a}", failCode: 9, previousCode: 8},
		{name: "literal shifted key", kpk: 2, keysyms: []xproto.Keysym{0xffe1, 0, 0x61, 0x41}, key: "A", failCode: 8},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "cleanup succeeds"},
			{name: "cleanup fails", releaseFails: true},
			{name: "canceled during failed press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("modifier write delivered then rejected")
				releaseErr := errors.New("failed uncertain modifier cleanup")
				b, events := newTestXTestBackend(t, method.kpk, method.keysyms, func(ev xtestEvent) error {
					if ev.eventType == xproto.KeyPress && ev.detail == method.failCode {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if ev.eventType == xproto.KeyRelease && ev.detail == method.failCode && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				var err error
				if method.previousCode != 0 {
					err = b.Type(ctx, method.key)
				} else {
					err = b.TypeLiteral(ctx, method.key)
				}
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press, cleanup or cancellation error: %v", err)
				}
				want := []xtestEvent{}
				if method.previousCode != 0 {
					want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: method.previousCode})
				}
				want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: method.failCode}, xtestEvent{eventType: xproto.KeyRelease, detail: method.failCode})
				if method.previousCode != 0 {
					want = append(want, xtestEvent{eventType: xproto.KeyRelease, detail: method.previousCode})
				}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("modifier events=%#v, want %#v", *events, want)
				}
			})
		}
	}
}
