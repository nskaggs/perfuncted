package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// The virtual Wayland backend must revoke an operation waiting behind a
// currently admitted operation. A revoked request must never acquire the
// shared display and inject messages after Close starts.
func TestWlVirtualCloseRevokesQueuedInputWithoutDispatch(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- b.withOperation(context.Background(), func(_ wl.Ctx, ctx context.Context) error {
			close(firstEntered)
			<-ctx.Done()
			<-releaseFirst
			return ctx.Err()
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first operation did not enter display")
	}
	queued := make(chan error, 1)
	dispatched := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(context.Background(), func(wl.Ctx, context.Context) error { dispatched <- struct{}{}; return nil })
	}()
	// Ensure the second caller has been admitted before initiating Close. A
	// preempted caller is allowed to be rejected at admission instead.
	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) && (err == nil || err.Error() != "input/wl-virtual: backend not initialised") && (err == nil || err.Error() != "input/wl-virtual: backend is closed") {
			t.Fatalf("queued request unexpectedly completed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued operation failed to drain")
	}
	select {
	case <-dispatched:
		t.Fatal("queued operation dispatched after close")
	default:
	}
	close(releaseFirst)
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active operation ignored shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active operation did not return")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked after both operations drained")
	}
	if msgs := readCapturedWlWrites(t, conn); len(msgs) != 0 {
		t.Fatalf("revoked operations emitted %d Wayland frames", len(msgs))
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}
