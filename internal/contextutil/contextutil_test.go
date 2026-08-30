package contextutil

import (
	"context"
	"testing"
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
