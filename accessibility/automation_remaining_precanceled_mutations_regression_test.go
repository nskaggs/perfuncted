package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A caller that has already canceled must not run parameter or handle
// validation, nor expose whether a provider has accepted the request.
func TestATSPIRemainingMutationEntryPointsRejectCanceledCallerBeforeValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"scroll point", func(c context.Context) error { return b.ScrollToPoint(c, id, CoordType(100), 0, 0) }},
		{"position", func(c context.Context) error { return b.SetPosition(c, id, 0, 0, CoordType(100)) }},
		{"extents", func(c context.Context) error { return b.SetExtents(c, id, 0, 0, 10, -1, CoordTypeScreen) }},
		{"delete text", func(c context.Context) error { return b.DeleteText(c, id, 4, 3) }},
		{"copy text", func(c context.Context) error { return b.CopyText(c, id, 4, 3) }},
		{"cut text", func(c context.Context) error { return b.CutText(c, id, 4, 3) }},
		{"paste text", func(c context.Context) error { return b.PasteText(c, id, -1) }},
		{"caret", func(c context.Context) error { return b.SetCaretOffset(c, id, -1) }},
		{"set selection", func(c context.Context) error { return b.SetTextSelection(c, id, -1, 0, 1) }},
		{"add selection", func(c context.Context) error { return b.AddTextSelection(c, id, 4, 3) }},
		{"remove selection", func(c context.Context) error { return b.RemoveTextSelection(c, id, -1) }},
		{"document selections", func(c context.Context) error { return b.SetTextSelections(c, id, nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request returned %v", err)
			}
			if err := tc.run(nil); err == nil {
				t.Fatal("nil context accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid canceled requests dispatched %d provider calls", calls)
	}
}
