package accessibility

import (
	"testing"
	"time"
)

// Two transitions of one property are not duplicates simply because they
// occur inside the coalescing window. For redacted values, the payload has
// deliberately been erased, so equality cannot establish duplication.
func TestATSPICoalescingNeverDropsDistinctOrRedactedStateTransitions(t *testing.T) {
	base := time.Now()
	node := NodeID{BusName: "org.example.App", ObjectPath: "/editor", Generation: 4}
	var key string
	var at time.Time
	first := Event{Kind: "object:property-change", Node: node, Property: "Name", Value: "Save", Timestamp: base}
	if coalesceEvent(&key, &at, first) {
		t.Fatal("first event suppressed")
	}
	duplicate := first
	duplicate.Timestamp = base.Add(time.Millisecond)
	if !coalesceEvent(&key, &at, duplicate) {
		t.Fatal("same-value duplicate not coalesced")
	}
	changed := first
	changed.Value = "Delete"
	changed.Timestamp = base.Add(2 * time.Millisecond)
	if coalesceEvent(&key, &at, changed) {
		t.Fatal("new value discarded as duplicate")
	}
	changed.Timestamp = base.Add(3 * time.Millisecond)
	if !coalesceEvent(&key, &at, changed) {
		t.Fatal("same new value not coalesced")
	}
	protected := first
	protected.Property = "CurrentValue"
	protected.Value = ""
	protected.Timestamp = base.Add(4 * time.Millisecond)
	if coalesceEvent(&key, &at, protected) {
		t.Fatal("first redacted event discarded")
	}
	protected.Timestamp = base.Add(5 * time.Millisecond)
	if coalesceEvent(&key, &at, protected) {
		t.Fatal("second redacted transition discarded with no way to prove duplication")
	}
	// Dropped duplicate timestamps must not prevent a later in-window state
	// change from being reported.
	after := changed
	after.Timestamp = base.Add(6 * time.Millisecond)
	if coalesceEvent(&key, &at, after) {
		t.Fatal("different property after secret transition discarded")
	}
}
