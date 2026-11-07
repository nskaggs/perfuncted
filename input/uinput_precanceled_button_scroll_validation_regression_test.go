//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// An already canceled uinput operation must not reject an invalid payload
// first. A canceled caller is not permitted to inject mouse events or
// distinguish individual payload validation results.
func TestUinputCanceledButtonAndScrollRequestsRejectBeforePayloadValidation(t *testing.T) {
	b := &UinputBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		invoke func(context.Context) error
	}{
		{"click", func(ctx context.Context) error { return b.MouseClick(ctx, 10, 10, 99) }},
		{"down", func(ctx context.Context) error { return b.MouseDown(ctx, 99) }},
		{"up", func(ctx context.Context) error { return b.MouseUp(ctx, 99) }},
		{"scroll up", func(ctx context.Context) error { return b.ScrollUp(ctx, -1) }},
		{"scroll down", func(ctx context.Context) error { return b.ScrollDown(ctx, -1) }},
		{"scroll left", func(ctx context.Context) error { return b.ScrollLeft(ctx, -1) }},
		{"scroll right", func(ctx context.Context) error { return b.ScrollRight(ctx, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.invoke(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled call returned %v instead of canceled", err)
			}
		})
	}
}
