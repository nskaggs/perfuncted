//go:build linux

package window

import (
	"context"
	"errors"
	"testing"
)

// The provider may return multiple windows in one KWin callback. Cancelling
// while consuming the first must not disclose subsequent window metadata.
func TestKWinPrivateBusIteratorStopsPublishingWindowsAfterConsumerCancellation(t *testing.T) {
	m, service := newSyntheticKWinManager(t)
	service.mu.Lock()
	service.listOverride = `[{"id":"7","title":"First private window"},{"id":"8","title":"Second private window"}]`
	service.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	observedCancel := false
	for info, err := range m.IterateWindows(ctx) {
		if count == 0 {
			if err != nil || info.Title != "First private window" {
				t.Fatalf("initial window missing: %+v %v", info, err)
			}
			count++
			cancel()
			continue
		}
		if errors.Is(err, context.Canceled) && info == (Info{}) {
			observedCancel = true
			break
		}
		t.Fatalf("KWin leaked window after consumer cancellation: %+v %v", info, err)
	}
	if count != 1 || !observedCancel {
		t.Fatalf("cancellation was not surfaced during enumeration: count=%d canceled=%v", count, observedCancel)
	}
}
