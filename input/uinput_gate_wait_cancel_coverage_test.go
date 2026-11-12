//go:build linux

package input

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// A canceled request must not enter a device operation while another
// operation owns the injection gate, even if its backend is still open.
func TestUinputWaitingForOperationGateHonorsCancellation(t *testing.T) {
	backend := &UinputBackend{}
	gate := backend.operationGateChannel()
	<-gate // simulate a device operation currently holding the gate
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- backend.withOperation(ctx, func(context.Context) error {
			entered <- struct{}{}
			return nil
		})
	}()
	// Give the admitted operation a chance to block on the occupied gate.
	// The gate remains unavailable throughout cancellation.
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting operation returned %v, want context cancellation", err)
	}
	select {
	case <-entered:
		t.Fatal("canceled operation entered device callback")
	default:
	}
	gate <- struct{}{}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}
