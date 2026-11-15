package input

import (
	"context"
	"errors"
	"testing"
)

// Transport-level mouse helpers must honor cancellation before examining an
// unavailable virtual device or emitting any pointer events. This also holds
// for calls made after the public dispatch layer's admission check.
func TestUinputInternalMouseHelpersRefuseCanceledDispatch(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"button down", func() error { return backend.mouseDown(ctx, 1) }},
		{"button up", func() error { return backend.mouseUp(ctx, 1) }},
		{"scroll", func() error { return backend.scroll(ctx, false, 1, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled helper reached device dispatch: %v", err)
			}
		})
	}
}
