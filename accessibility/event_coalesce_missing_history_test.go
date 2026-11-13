package accessibility

import (
	"testing"
	"time"
)

// Coalescing may be called before its state has been allocated. An absent
// history cannot prove an event is a duplicate and must not be dereferenced.
func TestEventCoalescingWithoutCompleteHistoryNeverDropsEvent(t *testing.T) {
	now := time.Now()
	key := "prior"
	at := now
	event := Event{Kind: "object:state-changed", Timestamp: now, Value: "on"}
	for _, tc := range []struct {
		name string
		key  *string
		at   *time.Time
	}{
		{name: "missing key", at: &at},
		{name: "missing timestamp", key: &key},
		{name: "both missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if coalesceEvent(tc.key, tc.at, event) {
				t.Fatal("event with incomplete history was dropped")
			}
		})
	}
}
