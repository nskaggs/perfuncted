package input

import (
	"context"
	"errors"
	"testing"
)

// Canceled portal operations lose authority before validating text, mouse
// coordinates or button numbers. Invalid arguments may not supersede the
// cancellation, and none of these operations may enqueue EIS frames.
func TestRemoteDesktopPreCanceledPointerAndLiteralPreflight(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"literal invalid UTF8", func(ctx context.Context) error { return b.TypeLiteral(ctx, string([]byte{0xff})) }},
		{"mouse move invalid region", func(ctx context.Context) error { return b.MouseMove(ctx, -999, -999) }},
		{"mouse click invalid button", func(ctx context.Context) error { return b.MouseClick(ctx, -999, -999, 19) }},
		{"mouse down invalid button", func(ctx context.Context) error { return b.MouseDown(ctx, 19) }},
		{"mouse up invalid button", func(ctx context.Context) error { return b.MouseUp(ctx, 19) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
			if err := tc.call(nil); err == nil || err.Error() != "input/remote-desktop: nil operation context" {
				t.Fatalf("nil context returned %v", err)
			}
		})
	}
}
