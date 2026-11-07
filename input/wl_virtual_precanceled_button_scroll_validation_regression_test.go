//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Cancellation must take precedence over invalid button or scroll payloads;
// a revoked operation cannot inspect arguments or mutate compositor input.
func TestWlVirtualCanceledButtonAndScrollRejectBeforePayloadValidation(t *testing.T) {
	b := &WlVirtualBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"button down", func(c context.Context) error { return b.MouseDown(c, 999) }},
		{"button up", func(c context.Context) error { return b.MouseUp(c, 999) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
		})
	}
}
