package accessibility

import (
	"context"
	"testing"
)

// A document selection's start and end offsets are ordered within one node.
// Different nodes may have incomparable local offsets; only same-node
// reversals are invalid and must not be dispatched to the provider.
func TestSetDocumentTextSelectionsRejectsReversedOffsetsWithinSameNode(t *testing.T) {
	calls := 0
	backend := &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		calls++
		return true, nil
	}}
	id := NodeID{BusName: "org.test", ObjectPath: "/document", Generation: 1}
	start := NodeID{BusName: "org.test", ObjectPath: "/start", Generation: 1}
	other := NodeID{BusName: "org.test", ObjectPath: "/other", Generation: 1}
	bad := []DocumentTextSelection{{StartObject: start, EndObject: start, StartOffset: 6, EndOffset: 2, StartIsActive: true}}
	if err := backend.SetTextSelections(context.Background(), id, bad); err == nil || calls != 0 {
		t.Fatalf("reversed same-node selection dispatched: err=%v calls=%d", err, calls)
	}
	valid := []DocumentTextSelection{{StartObject: start, EndObject: other, StartOffset: 6, EndOffset: 2, StartIsActive: true}}
	if err := backend.SetTextSelections(context.Background(), id, valid); err != nil || calls != 1 {
		t.Fatalf("distinct-node selection rejected: err=%v calls=%d", err, calls)
	}
}
