//go:build linux

package window

import (
	"context"
	"errors"
	"testing"
)

// A ready callback cannot authorize a fresh window observation if the
// operation expires between initial validation and callback publication.
type kwinCancelAfterFirstCheck struct {
	context.Context //nolint:containedctx // Controls an exact, synthetic cancellation boundary.
	checks          int
}

func (c *kwinCancelAfterFirstCheck) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestKWinReadyCallbackRejectedWhenCallerCanceledAfterAdmission(t *testing.T) {
	ctx := &kwinCancelAfterFirstCheck{Context: context.Background()}
	recv := &pfReceiver{ch: make(chan string, 1)}
	recv.ch <- `[{"id":"42","title":"stale observation"}]`
	value, err := waitForKWinResult(ctx, recv, 42)
	if value != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation published callback=%q err=%v", value, err)
	}
	if ctx.checks != 2 {
		t.Fatalf("cancellation checked %d times, want admission and publication", ctx.checks)
	}
}
