package window

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// FindByTitle returns the first window whose title contains substr
// (case-insensitive). Error messages are standardized for callers.
func FindByTitle(ctx context.Context, m Manager, substr string) (Info, error) {
	wins, err := m.List(ctx)
	if err != nil {
		return Info{}, err
	}
	lc := strings.ToLower(substr)
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), lc) {
			return w, nil
		}
	}
	return Info{}, fmt.Errorf("window matching %q not found", substr)
}

// WaitFor blocks until a window matching pattern is found, or ctx expires.
func WaitFor(ctx context.Context, m Manager, pattern string, poll time.Duration) (Info, error) {
	const (
		maxPoll      = 100 * time.Millisecond
		jitterFactor = 0.1
	)
	delay := poll
	if delay > maxPoll {
		delay = maxPoll
	}
	for {
		info, err := FindByTitle(ctx, m, pattern)
		if err == nil {
			return info, nil
		}

		// Calculate next delay with exponential backoff and jitter
		nextDelay := delay * 2
		if nextDelay > maxPoll {
			nextDelay = maxPoll
		}
		jitter := time.Duration(float64(nextDelay) * jitterFactor * (rand.Float64()*2 - 1))
		nextDelay += jitter
		if nextDelay < time.Millisecond {
			nextDelay = time.Millisecond
		}

		select {
		case <-ctx.Done():
			return Info{}, fmt.Errorf("wait for window %q: %w", pattern, ctx.Err())
		case <-time.After(delay):
			delay = nextDelay
		}
	}
}

// WaitForClose blocks until no window matches pattern, or ctx expires.
func WaitForClose(ctx context.Context, m Manager, pattern string, poll time.Duration) error {
	const (
		maxPoll      = 100 * time.Millisecond
		jitterFactor = 0.1
	)
	delay := poll
	if delay > maxPoll {
		delay = maxPoll
	}
	for {
		_, err := FindByTitle(ctx, m, pattern)
		if err != nil {
			return nil
		}

		// Calculate next delay with exponential backoff and jitter
		nextDelay := delay * 2
		if nextDelay > maxPoll {
			nextDelay = maxPoll
		}
		jitter := time.Duration(float64(nextDelay) * jitterFactor * (rand.Float64()*2 - 1))
		nextDelay += jitter
		if nextDelay < time.Millisecond {
			nextDelay = time.Millisecond
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for window close %q: %w", pattern, ctx.Err())
		case <-time.After(delay):
			delay = nextDelay
		}
	}
}
