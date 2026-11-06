//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A second input operation waiting for an already active operation must not
// inject its events if the caller cancels while waiting for the operation gate.
func TestUinputGateRejectsQueuedOperationAfterCallerCancellation(t *testing.T) {
	b := &UinputBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- b.withOperation(context.Background(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first input operation never acquired gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	executed := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(ctx, func(context.Context) error { executed <- struct{}{}; return nil })
	}()
	// Wait until both operations have entered the lifetime owner, ensuring the
	// second request is blocked at the gate rather than canceled before Begin.
	// Cancellation is also correct when the request has not yet reached Begin.
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued operation remained blocked")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first operation failed: %v", err)
	}
	select {
	case <-executed:
		t.Fatal("canceled operation injected device events")
	default:
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("gate not reusable after canceled waiter: %v", err)
	}
}

// Shutdown must cancel queued operations even when an earlier operation owns
// the serialized writer. The already-running operation must drain before Close
// returns, and no blocked command may run after the backend is closed.
func TestUinputGateShutdownCancelsQueuedOperationAndWaitsForActiveWriter(t *testing.T) {
	b := &UinputBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- b.withOperation(context.Background(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("active writer failed to start")
	}
	queued := make(chan error, 1)
	executed := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(context.Background(), func(context.Context) error { executed <- struct{}{}; return nil })
	}()
	closed := make(chan error, 1)
	go func() { closed <- b.operations.Close(nil) }()
	select {
	case err := <-closed:
		t.Fatalf("shutdown completed while active writer was still running: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to drain active writer")
	}
	if err := <-first; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("active canceled writer error=%v", err)
	}
	if err := <-queued; err == nil {
		t.Fatal("queued operation succeeded after shutdown")
	}
	select {
	case <-executed:
		t.Fatal("queued operation ran after shutdown")
	default:
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed backend admitted work: %v", err)
	}
}
