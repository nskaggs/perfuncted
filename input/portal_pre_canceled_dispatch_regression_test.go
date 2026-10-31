package input

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// An already-canceled caller must not enter the portal command queue even
// when its unbuffered receiver is ready at the same instant. Select alone
// does not give canceled contexts priority over other ready channels.
func TestRemoteDesktopPreCanceledOperationNeverEntersReadyCommandQueue(t *testing.T) {
	for i := 0; i < 128; i++ {
		b := syntheticQueuedPortal()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		receiverReady := make(chan struct{})
		stopReceiver := make(chan struct{})
		completed := make(chan struct{})
		var accepted atomic.Bool
		go func() {
			defer close(completed)
			close(receiverReady)
			select {
			case cmd := <-b.commands:
				accepted.Store(true)
				cmd.done <- nil
			case <-stopReceiver:
			}
		}()
		<-receiverReady
		err := b.submit(ctx, func() error { return nil })
		close(stopReceiver)
		<-completed
		if accepted.Load() {
			t.Fatalf("iteration %d: canceled caller entered the ready command queue", i)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: expected cancellation, got %v", i, err)
		}
	}
}
