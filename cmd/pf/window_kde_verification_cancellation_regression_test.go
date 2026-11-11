package main

import (
	"context"
	"errors"
	"testing"
)

// A stale compositor confirmation arriving with caller cancellation must not
// be reported as a successful KWin mutation verification.
func TestKDEStateWaiterRejectsSuccessAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := waitForKWinState(ctx, func(context.Context) (bool, error) {
		calls++
		cancel()
		return true, nil
	})
	if calls != 1 {
		t.Fatalf("probe count=%d, want 1", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation acknowledged as verified success: %v", err)
	}
}

// A successful probe cannot override its own simultaneous failure.
func TestKDEStateWaiterPreservesProbeErrorWhenMarkedReady(t *testing.T) {
	sentinel := errors.New("compositor disconnected during confirmation")
	err := waitForKWinState(context.Background(), func(context.Context) (bool, error) { return true, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost compositor failure: %v", err)
	}
}
