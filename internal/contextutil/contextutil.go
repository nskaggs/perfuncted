// Package contextutil contains implementation-only context helpers.
package contextutil

import (
	"context"
	"errors"
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

// ErrorWithCause keeps the standard cancellation/deadline category
// while retaining a caller-supplied cancellation cause for diagnostics.
func ErrorWithCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	err := ctx.Err()
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, err) {
		return err
	}
	return errors.Join(err, cause)
}
