package window

import (
	"context"
	"errors"
	"testing"
)

func TestSwayIteratorDoesNotPublishLaterWindowsAfterCancellation(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"first"},{"id":43,"type":"con","name":"private-second"}]}`
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	canceled := false
	for info, err := range manager.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if info.ID != 0 {
				t.Fatalf("cancellation yielded private window %#v", info)
			}
			canceled = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count != 1 || info.Title != "first" {
			t.Fatalf("window yielded after cancellation: %#v", info)
		}
		cancel()
	}
	if count != 1 || !canceled {
		t.Fatalf("yielded %d windows, cancellation=%v; want one window then cancellation", count, canceled)
	}
}

func TestSwayIteratorNilContextUsesDefaultContext(t *testing.T) {
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(`{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"app"}]}`))}
	seen := 0
	for info, err := range manager.IterateWindows(nil) {
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != 42 || info.Title != "app" {
			t.Fatalf("unexpected default context result: %+v", info)
		}
		seen++
	}
	if seen != 1 {
		t.Fatalf("got %d windows, want 1", seen)
	}
}
