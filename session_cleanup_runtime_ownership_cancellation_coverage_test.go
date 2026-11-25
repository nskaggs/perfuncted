package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cancelAtErrCallContext struct {
	context.Context
	cancel context.CancelCauseFunc
	target int
	calls  int
}

func (c *cancelAtErrCallContext) Err() error {
	c.calls++
	if c.calls == c.target {
		c.cancel(errors.New("caller withdrew cleanup authority"))
	}
	return c.Context.Err()
}

func TestRateLimitedSessionCleanupPreservesConcurrentCancellation(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	previousTime, previousAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = previousTime, previousAttempt
		cleanupStaleSessionsMu.Unlock()
	})

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	err := CleanupStaleSessionsContext(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("rate-limited cleanup error = %v, want caller cancellation and cause", err)
	}
}
