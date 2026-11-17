package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func TestSleepContextReportsElapsedDeadlineBeforeContextCallback(t *testing.T) {
	ctx := pendingDeadlineContext{
		Context:  context.Background(),
		deadline: time.Now().Add(-time.Second),
	}
	if err := sleepContext(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleep with elapsed deadline returned %v, want deadline exceeded", err)
	}
}
