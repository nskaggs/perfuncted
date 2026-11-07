//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Revoked input authority wins over malformed key, button, and scroll
// arguments. Validation must not bypass the operation lifetime boundary.
func TestGnomePreCanceledInputsRejectBeforeArgumentValidation(t *testing.T) {
	b := &GnomeNativeBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{"key down", func(c context.Context) error { return b.KeyDown(c, "not_a_key_name") }},
		{"key up", func(c context.Context) error { return b.KeyUp(c, "not_a_key_name") }},
		{"mouse down", func(c context.Context) error { return b.MouseDown(c, 99) }},
		{"mouse up", func(c context.Context) error { return b.MouseUp(c, 99) }},
		{"mouse click", func(c context.Context) error { return b.MouseClick(c, 1, 2, 99) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range operations {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled input returned %v", err)
			}
		})
	}
}
