package contextutil

import (
	"context"
	"errors"
)

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
