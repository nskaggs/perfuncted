//go:build linux

package window

import (
	"context"
	"errors"
	"testing"
)

// A callback can arrive concurrently with caller cancellation. Once canceled,
// the caller must not receive a "verified" window list from an expired query.
func TestKWinWaitForResultReadyCallbackMustNotOutrunCanceledCaller(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		receiver := &pfReceiver{ch: make(chan string, 1)}
		receiver.ch <- `[{"id":"7","title":"stale"}]`
		cancel()
		response, err := waitForKWinResult(ctx, receiver, 7)
		if !errors.Is(err, context.Canceled) || response != "" {
			t.Fatalf("iteration %d accepted stale callback after cancellation: response=%q error=%v", i, response, err)
		}
	}
}
