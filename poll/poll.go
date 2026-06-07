package poll

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/ctxutil"
)

// ErrNilFunction is returned when a poll helper receives a nil callback.
var ErrNilFunction = errors.New("nil function")

// AdaptivePoll returns an exponentially backing-off poll duration. base is
// the starting duration and max is the cap.
func AdaptivePoll(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if base <= 0 || max <= 0 {
		return 0
	}
	if base >= max {
		return max
	}

	d := base
	for range attempt {
		if d > max-d {
			return max
		}
		d *= 2
	}
	return d
}

func clamp(d time.Duration) time.Duration {
	if d <= 0 {
		return 10 * time.Millisecond
	}
	return d
}

// WaitFunc polls fn every poll interval until fn returns a value with no error,
// or ctx is done.
func WaitFunc[T any](ctx context.Context, pollInterval time.Duration, fn func() (T, error)) (T, error) {
	ctx = ctxutil.Default(ctx)
	if fn == nil {
		var zero T
		return zero, fmt.Errorf("poll.WaitFunc: %w", ErrNilFunction)
	}
	pollInterval = clamp(pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		select {
		case <-ctx.Done():
			var zero T
			return zero, fmt.Errorf("poll.WaitFunc: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
