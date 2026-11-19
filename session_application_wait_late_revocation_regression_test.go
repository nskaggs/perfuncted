package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// Returning a completed child is not authority to report success if the
// caller's cancellation arrives between wait preflight and result admission.
func TestApplicationWaitRechecksRevocationAfterChildCompletion(t *testing.T) {
	done := make(chan struct{})
	close(done)
	app := &Application{done: done}
	for attempt := 0; attempt < 100; attempt++ {
		base, cancel := context.WithCancelCause(context.Background())
		ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
		err := app.Wait(ctx)
		if !errors.Is(err, context.Canceled) || ctx.calls != 2 {
			t.Fatalf("late cancellation after child completion: err=%v checks=%d", err, ctx.calls)
		}
		cancel(nil)
	}
}

type cancelWhenWaitSelectsContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c *cancelWhenWaitSelectsContext) Done() <-chan struct{} {
	c.cancel()
	return c.Context.Done()
}

// Revocation after the first preflight, while waiting for a live child, must
// complete the wait without requiring that the child exit.
func TestApplicationWaitRevokedDuringLiveChildObservation(t *testing.T) {
	app := &Application{done: make(chan struct{})}
	for attempt := 0; attempt < 100; attempt++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelWhenWaitSelectsContext{Context: base, cancel: cancel}
		if err := app.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("live child wait continued after owner cancellation: %v", err)
		}
		select {
		case <-app.done:
			t.Fatal("canceling wait altered child completion")
		default:
		}
	}
}
