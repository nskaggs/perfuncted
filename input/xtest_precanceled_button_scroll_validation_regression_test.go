package input

import (
	"context"
	"errors"
	"testing"
)

// A caller that has already canceled may not get a different outcome from
// malformed button or scroll arguments. The XTEST connection is deliberately
// absent; none of these calls may reach its transport.
func TestXTestPreCanceledPointerAndScrollRejectBeforeValidation(t *testing.T) {
	b := &XTestBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"click", func(c context.Context) error { return b.MouseClick(c, 10, 20, 99) }},
		{"down", func(c context.Context) error { return b.MouseDown(c, 99) }},
		{"up", func(c context.Context) error { return b.MouseUp(c, 99) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled operation returned %v", err)
			}
		})
	}
}
