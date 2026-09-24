package contextutil

import (
	"context"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	var nilCtx context.Context
	if got := Default(nilCtx); got == nil {
		t.Fatal("Default(nil) = nil, want background context")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if got := Default(ctx); got != ctx {
		t.Fatalf("Default(ctx) = %v, want %v", got, ctx)
	}
}

func TestWithTimeoutFallbackUsesCallerDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	bounded, boundedCancel := WithTimeoutFallback(ctx, time.Second)
	defer boundedCancel()
	got, ok := bounded.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("deadline = %v, present=%v, want caller deadline %v", got, ok, deadline)
	}
}

func TestWithTimeoutFallbackBoundsContextWithoutDeadline(t *testing.T) {
	bounded, cancel := WithTimeoutFallback(context.Background(), time.Minute)
	defer cancel()

	deadline, ok := bounded.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= 0 || remaining > time.Minute {
		t.Fatalf("fallback deadline remaining = %v, present=%v, want positive and <= 1m", remaining, ok)
	}
}

func TestWithTimeoutFallbackPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bounded, boundedCancel := WithTimeoutFallback(ctx, time.Minute)
	defer boundedCancel()

	cancel()
	if err := bounded.Err(); err != context.Canceled {
		t.Fatalf("bounded context error = %v, want context.Canceled", err)
	}
}
