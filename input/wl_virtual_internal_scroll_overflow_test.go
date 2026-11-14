package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Scroll entry points validate public notation, but the backend's shared
// protocol encoder must also enforce its signed 24.8 representation limit.
// In particular, a negative overflow must fail before dereferencing an
// uninitialized compositor transport or sending any wire messages.
func TestWlVirtualScrollEncoderRejectsSignedOverflowBeforeTransport(t *testing.T) {
	b := &WlVirtualBackend{}
	for _, clicks := range []int{-maxScrollClicks - 1, maxScrollClicks + 1} {
		err := b.scroll(context.Background(), 0, clicks)
		if err == nil || !strings.Contains(err.Error(), "scroll clicks must be at most") {
			t.Fatalf("unbounded scroll %d reached transport: %v", clicks, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.scroll(ctx, 0, maxScrollClicks+1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scroll dispatched or lost cancellation precedence: %v", err)
	}
}
