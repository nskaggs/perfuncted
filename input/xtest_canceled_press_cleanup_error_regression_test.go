//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestXTestCanceledPressPreservesCancellationWhenReleaseFails(t *testing.T) {
	releaseErr := errors.New("synthetic input release failure")
	tests := []struct {
		name  string
		call  func(*XTestBackend, context.Context) error
		press xtestEvent
		up    xtestEvent
		want  []xtestEvent
	}{
		{
			name: "mouse click",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.MouseClick(ctx, 4, 7, 1)
			},
			press: xtestEvent{eventType: xproto.ButtonPress, detail: 1},
			up:    xtestEvent{eventType: xproto.ButtonRelease, detail: 1},
			want: []xtestEvent{
				{eventType: xproto.MotionNotify},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
			},
		},
		{
			name: "typed key",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.Type(ctx, "{a}")
			},
			press: xtestEvent{eventType: xproto.KeyPress, detail: 8},
			up:    xtestEvent{eventType: xproto.KeyRelease, detail: 8},
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
			},
		},
		{
			name: "literal text",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.TypeLiteral(ctx, "a")
			},
			press: xtestEvent{eventType: xproto.KeyPress, detail: 8},
			up:    xtestEvent{eventType: xproto.KeyRelease, detail: 8},
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
				if event == test.press {
					cancel()
				}
				if event == test.up {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })

			err := test.call(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled action error = %v, want context.Canceled", err)
			}
			if !errors.Is(err, releaseErr) {
				t.Fatalf("canceled action error = %v, want release failure", err)
			}
			if got := *events; !sameXTestEvents(got, test.want) {
				t.Fatalf("press/release cleanup events = %#v", got)
			}
		})
	}
}
