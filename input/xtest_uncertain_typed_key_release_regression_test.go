//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// After a successful key press, a failed release may never have reached the
// X server. Retry cleanup before returning instead of leaving a key held.
func TestXTestTypedKeyReleaseFailureRetriesCleanup(t *testing.T) { //nolint:gocyclo // Two typing APIs and two release failure modes.
	for _, method := range []struct {
		name string
		call func(*XTestBackend) error
	}{
		{name: "literal", call: func(b *XTestBackend) error { return b.TypeLiteral(context.Background(), "a") }},
		{name: "key syntax", call: func(b *XTestBackend) error { return b.Type(context.Background(), "{a}") }},
	} {
		for _, tc := range []struct {
			name      string
			permanent bool
		}{
			{name: "transient release failure"},
			{name: "permanent release failure", permanent: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				firstErr := errors.New("key release not acknowledged")
				cleanupErr := errors.New("compensating key release failed")
				releases := 0
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
					if ev.eventType == xproto.KeyRelease {
						releases++
						if releases == 1 {
							return firstErr
						}
						if tc.permanent {
							return cleanupErr
						}
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := method.call(b)
				if !errors.Is(err, firstErr) || errors.Is(err, cleanupErr) != tc.permanent {
					t.Fatalf("key release errors not preserved: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.KeyPress, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("release events=%#v want=%#v", *events, want)
				}
			})
		}
	}
}
