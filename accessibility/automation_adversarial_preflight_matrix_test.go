package accessibility

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// Check every public mutation's representability boundary at the provider
// dispatch seam. These invalid requests must be rejected before any AT-SPI
// wire operation, even if the provider would otherwise acknowledge them.
func TestATSPIMutationPreflightRejectsUnrepresentableCommandsWithoutDispatch(t *testing.T) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/widget", Generation: 3}
	high := int(math.MaxInt32) + 1
	low := int(math.MinInt32) - 1
	cases := []struct {
		name     string
		invoke   func(*dbusBackend) error
		fragment string
	}{
		{"scroll type out of enum", func(b *dbusBackend) error { return b.ScrollTo(context.Background(), id, ScrollType(700)) }, "invalid scroll type"},
		{"scroll point invalid coordinate enum", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordType(90), 1, 2) }, "invalid coordinate type"},
		{"scroll point x underflow", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordTypeScreen, low, 3) }, "out of range"},
		{"scroll point y overflow", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordTypeScreen, 3, high) }, "out of range"},
		{"position coordinate enum", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, 1, 2, CoordType(100)) }, "invalid coordinate type"},
		{"position negative overflow", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, low, 1, CoordTypeParent) }, "out of range"},
		{"position positive overflow", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, 1, high, CoordTypeParent) }, "out of range"},
		{"size negative width", func(b *dbusBackend) error { return b.SetSize(context.Background(), id, -1, 200) }, "size out of range"},
		{"size positive overflow", func(b *dbusBackend) error { return b.SetSize(context.Background(), id, high, 200) }, "size out of range"},
		{"extents invalid enum", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, 1, 2, 300, 200, CoordType(99))
		}, "invalid coordinate type"},
		{"extents negative height", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, 1, 2, 300, -1, CoordTypeScreen)
		}, "extents out of range"},
		{"extents x overflow", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, high, 2, 300, 200, CoordTypeWindow)
		}, "extents out of range"},
		{"paste negative position", func(b *dbusBackend) error { return b.PasteText(context.Background(), id, -1) }, "invalid paste position"},
		{"caret negative offset", func(b *dbusBackend) error { return b.SetCaretOffset(context.Background(), id, -1) }, "invalid caret offset"},
		{"text selection negative id", func(b *dbusBackend) error { return b.SetTextSelection(context.Background(), id, -1, 0, 3) }, "invalid text selection"},
		{"text selection reverse offsets", func(b *dbusBackend) error { return b.SetTextSelection(context.Background(), id, 0, 5, 3) }, "before start"},
		{"add selection negative", func(b *dbusBackend) error { return b.AddTextSelection(context.Background(), id, -1, 3) }, "invalid text selection"},
		{"add selection reverse", func(b *dbusBackend) error { return b.AddTextSelection(context.Background(), id, 5, 3) }, "before start"},
		{"remove selection negative", func(b *dbusBackend) error { return b.RemoveTextSelection(context.Background(), id, -1) }, "invalid selection index"},
		{"delete text reverse", func(b *dbusBackend) error { return b.DeleteText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"copy text reverse", func(b *dbusBackend) error { return b.CopyText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"cut text reverse", func(b *dbusBackend) error { return b.CutText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"set documents empty", func(b *dbusBackend) error { return b.SetTextSelections(context.Background(), id, nil) }, "must not be empty"},
		{"set documents invalid start", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: NodeID{BusName: id.BusName, ObjectPath: "/bad", Generation: 99}, EndObject: id}})
		}, "start"},
		{"set documents invalid end", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: NodeID{BusName: id.BusName, ObjectPath: "/bad", Generation: 99}}})
		}, "end"},
		{"set documents negative offset", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: id, StartOffset: -1}})
		}, "negative offset"},
		{"set documents reverse same-node", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: id, StartOffset: 7, EndOffset: 2}})
		}, "before start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{generation: 3, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
			err := tc.invoke(b)
			if err == nil || !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("got %v, want error containing %q", err, tc.fragment)
			}
			if calls != 0 {
				t.Fatalf("invalid request dispatched %d provider mutations", calls)
			}
		})
	}
}

// A failure during command processing must not be disguised as a successful
// accepted mutation, and should retain unsupported/error identity.
func TestATSPIMutationProviderFailuresAndCanceledOperationsRemainObservable(t *testing.T) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/widget", Generation: 3}
	providerErr := errors.New("synthetic AT-SPI provider rejects mutation")
	for _, tc := range []struct {
		name   string
		invoke func(*dbusBackend) error
	}{
		{"grab focus", func(b *dbusBackend) error { return b.GrabFocus(context.Background(), id) }},
		{"set value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, 1.25) }},
		{"scroll to", func(b *dbusBackend) error { return b.ScrollTo(context.Background(), id, ScrollTopLeft) }},
		{"text insertion", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "abcd") }},
		{"selection", func(b *dbusBackend) error { return b.SelectChild(context.Background(), id, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			b := &dbusBackend{generation: 3, callOverride: func(context.Context, NodeID, string, []any) (any, error) { count++; return nil, providerErr }}
			if err := tc.invoke(b); !errors.Is(err, providerErr) {
				t.Fatalf("provider error identity was lost: %v", err)
			}
			if count != 1 {
				t.Fatalf("provider dispatched %d times", count)
			}
		})
	}
}
