package ctxutil

import (
	"context"
	"testing"
	"time"
)

func TestDefault_nonNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	got := Default(ctx)
	if got != ctx {
		t.Fatal("expected same context back")
	}
}

func TestDefault_nil(t *testing.T) {
	var nilCtx context.Context
	got := Default(nilCtx)
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	if got != context.Background() {
		t.Fatal("expected context.Background()")
	}
}

func TestDefault_deadlinePreserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := Default(ctx)
	deadline, ok := got.Deadline()
	if !ok {
		t.Fatal("expected deadline to be preserved")
	}
	if time.Until(deadline) > 5*time.Second {
		t.Fatal("deadline too far in the future")
	}
}

func TestDefault_valuePreserved(t *testing.T) {
	ctx := context.WithValue(context.Background(), key("k"), "v")
	got := Default(ctx)
	if v := got.Value(key("k")); v != "v" {
		t.Fatalf("expected 'v', got %v", v)
	}
}

func TestDefault_canceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Default(ctx)
	if err := got.Err(); err != context.Canceled {
		t.Fatalf("expected Canceled, got %v", err)
	}
}

type key string
