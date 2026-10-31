package accessibility

import (
	"testing"
	"time"
)

// A clock discontinuity or out-of-order delivery is not a duplicate inside
// a positive coalescing window. Suppressing it can discard the only current
// accessibility state transition for a watched control.
func TestEventCoalescingRequiresNonnegativeElapsedTime(t *testing.T) {
	now := time.Now()
	event := Event{Kind: "object:state-changed", Node: NodeID{BusName: "org.example.App", ObjectPath: "/button", Generation: 1}, Property: "focused", Timestamp: now}
	var key string
	var last time.Time
	if coalesceEvent(&key, &last, event) {
		t.Fatal("initial event suppressed")
	}
	earlier := event
	earlier.Timestamp = now.Add(-time.Second)
	if coalesceEvent(&key, &last, earlier) {
		t.Fatal("out-of-order event suppressed")
	}
	if !last.Equal(earlier.Timestamp) {
		t.Fatalf("out-of-order event did not reset reference time: %v", last)
	}
	duplicate := earlier
	duplicate.Timestamp = earlier.Timestamp.Add(eventCoalesceWindow / 2)
	if !coalesceEvent(&key, &last, duplicate) {
		t.Fatal("legitimate close duplicate not coalesced")
	}
	distinct := duplicate
	distinct.Property = "enabled"
	if coalesceEvent(&key, &last, distinct) {
		t.Fatal("distinct state property suppressed")
	}
}
