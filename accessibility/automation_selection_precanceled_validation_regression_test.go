package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A selection mutation with a canceled caller must not evaluate invalid
// indices or dispatch a provider command.
func TestATSPISelectionMutationsHonorPriorCancellationBeforeIndexValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"select child", func(c context.Context) error { return b.SelectChild(c, id, -1) }},
		{"deselect child", func(c context.Context) error { return b.DeselectChild(c, id, -1) }},
		{"select row", func(c context.Context) error { return b.SelectRow(c, id, -1) }},
		{"deselect row", func(c context.Context) error { return b.DeselectRow(c, id, -1) }},
		{"select column", func(c context.Context) error { return b.SelectColumn(c, id, -1) }},
		{"deselect column", func(c context.Context) error { return b.DeselectColumn(c, id, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation returned %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("canceled mutations dispatched %d provider commands", calls)
	}
}
