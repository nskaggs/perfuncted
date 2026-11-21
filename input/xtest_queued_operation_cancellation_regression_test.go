package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A queued XTEST operation must not execute once its owner cancels, even if
// the backend itself remains alive and another operation owns the gate.
// Use a held callback for a deterministic admission boundary, not X11 timing.
func TestXTestQueuedOperationCanceledBeforeTransportDispatch(t *testing.T) {
	backend := &XTestBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- backend.withOperation(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("initial XTEST operation never acquired gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	invoked := make(chan struct{}, 1)
	queued := make(chan error, 1)
	go func() {
		queued <- backend.withOperation(ctx, func(context.Context) error {
			invoked <- struct{}{}
			return nil
		})
	}()
	// Cancellation must terminate a queued operation while gate is still held.
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled XTEST operation remained queued")
	}
	select {
	case <-invoked:
		t.Fatal("canceled request reached XTEST transport")
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("unrelated admitted operation failed: %v", err)
	}
	if err := backend.withOperation(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("cancellation poisoned subsequent operations: %v", err)
	}
}
