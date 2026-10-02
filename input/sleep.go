package input

import (
	"context"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// sleepContext waits for d or until ctx is done, whichever happens first.
//
// Go delivers a context deadline from a timer callback, which can be delayed
// behind other runnable work, while this timer is a value the select already
// has in hand. When a caller's deadline and d both elapse while the goroutine
// is descheduled, select is free to choose the timer case even though the
// deadline has passed. Comparing the deadline against wall-clock time keeps a
// wait that ran past its deadline reporting that fact on a loaded machine.
func sleepContext(ctx context.Context, d time.Duration) error {
	ctx = contextutil.Default(ctx)
	if d <= 0 {
		return contextOutcome(ctx)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return contextOutcome(ctx)
	case <-t.C:
		return contextOutcome(ctx)
	}
}

// contextOutcome reports ctx's own error, or DeadlineExceeded when ctx carries
// a deadline that wall-clock time has already passed. A context whose deadline
// callback has not yet been delivered must not read as "no error".
func contextOutcome(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
