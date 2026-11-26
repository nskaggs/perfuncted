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

func TestNoninteractiveStartupRestrictionSurvivesDerivedContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	if NoninteractiveStartup(parent) || NoninteractiveStartup(nil) {
		t.Fatal("ordinary callers were restricted")
	}
	restricted := WithNoninteractiveStartup(parent)
	derived, stop := context.WithTimeout(restricted, time.Second)
	defer stop()
	if !NoninteractiveStartup(derived) || NoninteractiveStartup(parent) {
		t.Fatal("restriction was lost or leaked to parent")
	}
	cancel()
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatal("restriction severed parent cancellation")
	}
}
