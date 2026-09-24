// Package contextutil contains implementation-only context helpers.
package contextutil

import (
	"context"
	"time"
)

// Default returns ctx when non-nil and a background context otherwise.
func Default(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// WithTimeoutFallback bounds direct calls that do not carry a deadline while
// leaving a caller or session deadline authoritative when one is present.
func WithTimeoutFallback(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx = Default(ctx)
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
