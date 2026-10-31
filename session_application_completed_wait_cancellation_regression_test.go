package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A completed application and a canceled caller are both ready in the select.
// Cancellation must have priority so an already-canceled automation request
// does not randomly claim that waiting completed successfully.
func TestApplicationWaitCompletedProcessDoesNotOverrideCanceledCaller(t *testing.T) {
	completed := make(chan struct{})
	close(completed)
	app := &Application{done: completed}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if err := app.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Wait returned %v on completed process at attempt %d", err, i)
		}
	}
}

func TestApplicationWaitCompletedProcessPreservesExitAndDeadlineErrors(t *testing.T) {
	done := make(chan struct{})
	close(done)
	failure := errors.New("synthetic process failed")
	app := &Application{done: done, err: failure, path: "/synthetic/app"}
	if err := app.Wait(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("completed process exit error changed: %v", err)
	}
	if app.PID() != 0 || app.Path() != "/synthetic/app" || !app.Exited() {
		t.Fatal("completed process metadata wrong")
	}
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := app.Wait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired wait context returned %v", err)
	}
	if err := app.Wait(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil Wait context error=%v", err)
	}
	var missing *Application
	if missing.Path() != "" || !missing.Exited() || missing.PID() != 0 || missing.Wait(context.Background()) != nil || missing.Stop(context.Background()) != nil || missing.Kill() != nil {
		t.Fatal("nil application failed safe lifecycle contract")
	}
}
