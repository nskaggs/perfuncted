package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A request that already lost its lifetime cannot validate or dispatch
// provider mutations, even when the payload is malformed. Cancellation
// must be the authoritative failure at every public mutation boundary.
func TestATSPIInvalidMutationPayloadCannotOverridePriorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"invalid scroll enum", func(ctx context.Context) error { return b.ScrollTo(ctx, id, ScrollType(900)) }},
		{"negative size", func(ctx context.Context) error { return b.SetSize(ctx, id, -5, 10) }},
		{"NUL text", func(ctx context.Context) error { return b.SetTextContents(ctx, id, "hidden\x00text") }},
		{"negative insertion offset", func(ctx context.Context) error { return b.InsertText(ctx, id, -1, "hidden") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled mutation returned %v", err)
			}
			if err := tc.call(nil); err == nil {
				t.Fatal("nil context accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("canceled mutations dispatched %d provider operations", calls)
	}
}
