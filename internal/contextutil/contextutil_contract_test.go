package contextutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultPreservesCallerContextAndFallsBackOnNil(t *testing.T) {
	if Default(nil) == nil {
		t.Fatal("nil context fallback")
	}
	key := struct{}{}
	parent := context.WithValue(context.Background(), key, "present")
	if got := Default(parent).Value(key); got != "present" {
		t.Fatalf("parent value lost: %v", got)
	}
}

func TestWithTimeoutFallbackPreservesCallerDeadlineAndCancellation(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	parent, stop := context.WithDeadline(context.Background(), deadline)
	defer stop()
	derived, cancel := WithTimeoutFallback(parent, time.Nanosecond)
	defer cancel()
	actual, ok := derived.Deadline()
	if !ok || !actual.Equal(deadline) {
		t.Fatalf("caller deadline overridden: %v, %v", actual, ok)
	}
	if err := derived.Err(); err != nil {
		t.Fatalf("premature cancellation: %v", err)
	}
	cancel() // fallback cancel must not cancel caller-owned context
	if err := parent.Err(); err != nil {
		t.Fatalf("parent canceled: %v", err)
	}
	stop()
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatalf("caller cancellation not propagated: %v", derived.Err())
	}
}

func TestWithTimeoutFallbackCreatesBoundedContextWithNilParent(t *testing.T) {
	ctx, cancel := WithTimeoutFallback(nil, time.Hour)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("missing fallback deadline")
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("cancel not propagated: %v", ctx.Err())
	}
}

func TestWithTimeoutFallbackPreservesAlreadyCanceledParent(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	stop()
	ctx, cancel := WithTimeoutFallback(parent, time.Hour)
	defer cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("cancellation lost: %v", ctx.Err())
	}
}
