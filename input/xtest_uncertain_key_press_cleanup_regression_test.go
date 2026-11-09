//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// XTEST can deliver KeyPress before reporting a protocol error. A typed key
// must receive a compensating KeyRelease even when its press failed.
func TestXTestTypedKeyCompensatesForUncertainPress(t *testing.T) {
	for _, method := range []struct {
		name string
		call func(*XTestBackend, context.Context) error
	}{
		{name: "literal", call: func(b *XTestBackend, ctx context.Context) error { return b.TypeLiteral(ctx, "a") }},
		{name: "key syntax", call: func(b *XTestBackend, ctx context.Context) error { return b.Type(ctx, "{a}") }},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "release succeeds"},
			{name: "release fails", releaseFails: true},
			{name: "cancellation during press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XTEST key press may have been delivered")
				releaseErr := errors.New("XTEST key cleanup failed")
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
					if event.eventType == xproto.KeyPress {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if event.eventType == xproto.KeyRelease && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := method.call(b, ctx)
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("press/cleanup/cancellation result: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.KeyPress, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("uncompensated key events: got %#v, want %#v", *events, want)
				}
			})
		}
	}
}
