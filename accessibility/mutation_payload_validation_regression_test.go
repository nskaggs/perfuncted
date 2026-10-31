package accessibility

import (
	"context"
	"math"
	"testing"
)

// Provider mutation validation precedes dispatch. Non-finite values and
// malformed strings cannot be represented as meaningful AT-SPI state and
// must not reach D-Bus even when a faulty provider would acknowledge them.
func TestAccessibilityMutationRejectsNonFiniteValuesAndMalformedTextBeforeWire(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	for _, tc := range []struct {
		name string
		run  func(*dbusBackend) error
	}{
		{"NaN value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.NaN()) }},
		{"positive infinite value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.Inf(1)) }},
		{"negative infinite value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.Inf(-1)) }},
		{"NUL in full text", func(b *dbusBackend) error { return b.SetTextContents(context.Background(), id, "safe\x00hidden") }},
		{"invalid UTF8 in full text", func(b *dbusBackend) error { return b.SetTextContents(context.Background(), id, "\xff") }},
		{"NUL in inserted text", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "safe\x00hidden") }},
		{"invalid UTF8 in inserted text", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "\xff") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
			if err := tc.run(b); err == nil {
				t.Error("malformed mutation accepted")
			}
			if calls != 0 {
				t.Errorf("malformed mutation reached provider %d times", calls)
			}
		})
	}
}

func TestAccessibilityMutationAcceptsFiniteValuesAndUnicodeText(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	calls := 0
	b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	if err := b.SetValue(context.Background(), id, -123.456); err != nil {
		t.Fatal(err)
	}
	if err := b.SetTextContents(context.Background(), id, "Café 日本"); err != nil {
		t.Fatal(err)
	}
	if err := b.InsertText(context.Background(), id, 0, "Café"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 permitted mutations, got %d", calls)
	}
}
