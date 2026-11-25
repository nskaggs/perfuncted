package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Polling can exhaust its retry budget at the same instant startup loses
// authority. The final cancellation check must win over a timeout report.
func TestReadinessExhaustedBudgetPreservesLateOwnerCancellation(t *testing.T) {
	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 3}
	calls := 0
	err := PollCondition(ctx, 1, time.Millisecond, func() bool {
		calls++
		return false
	})
	if !errors.Is(err, context.Canceled) || context.Cause(base) == nil || calls != 1 || ctx.calls != 3 {
		t.Fatalf("last readiness check returned %v (checks=%d probes=%d), want cancellation", err, ctx.calls, calls)
	}
}

// A child-completion notification can race with cancellation. Rechecking
// authority both before and after acquiring the wait-result lock prevents a
// stale, apparently successful child wait from overriding caller cancellation.
func TestCompletedSessionProcessWaitRechecksCancellationAtBothAdmissionBoundaries(t *testing.T) {
	for _, targetCheck := range []int{2, 3} {
		base, cancel := context.WithCancelCause(context.Background())
		done := make(chan struct{})
		close(done)
		ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: targetCheck}
		process := &SessionProcess{waitDone: done}
		err := process.Wait(ctx, errInvalidArgument)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) || ctx.calls < targetCheck {
			t.Fatalf("cancel at check %d: got %v (checks=%d), want cancellation", targetCheck, err, ctx.calls)
		}
		cancel(nil)
	}
}

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
