package input

import (
	"context"
	"errors"
	"testing"
)

func TestRemoteDesktopZeroDistanceScrollDoesNotAcknowledgeCanceledOrNilOperation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil operation context", ctx: nil, want: ErrRemoteDesktopUnavailable},
		{name: "pre-canceled operation", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(), want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			for _, run := range []func(context.Context, int) error{b.ScrollUp, b.ScrollDown, b.ScrollLeft, b.ScrollRight} {
				err := run(tc.ctx, 0)
				if tc.ctx == nil {
					if err == nil {
						t.Fatal("nil context zero-scroll reported success")
					}
				} else if !errors.Is(err, tc.want) {
					t.Fatalf("canceled zero scroll reported success: %v", err)
				}
			}
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 {
				t.Fatalf("zero scroll changed input state: keys=%v buttons=%v", b.heldKeys, b.buttons)
			}
		})
	}
}
