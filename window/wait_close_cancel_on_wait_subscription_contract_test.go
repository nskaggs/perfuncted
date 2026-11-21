package window

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Cancellation can arrive exactly as the waiter subscribes after confirming
// that its target is still present. The wait must return immediately, without
// waiting for another polling tick or mistaking cancellation for closure.
type cancelAtWindowWaitContext struct {
	context.Context //nolint:containedctx // Test context triggers cancellation at the subscription boundary.
	cancel          context.CancelFunc
	subscriptions   int
}

func (c *cancelAtWindowWaitContext) Done() <-chan struct{} {
	c.subscriptions++
	c.cancel()
	return c.Context.Done()
}

func TestWaitForMatchCloseCancelsWhenWaitSubscriptionBegins(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtWindowWaitContext{Context: base, cancel: cancel}
	manager := &fakeManager{wins: []Info{{ID: 17, Title: "still-active-editor"}}}
	start := time.Now()
	err := WaitForMatchClose(ctx, manager, Match{TitleContains: "active-editor"}, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting for still-present target did not report cancellation: %v", err)
	}
	if ctx.subscriptions != 1 {
		t.Fatalf("wait subscribed %d times, want one", ctx.subscriptions)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation waited for polling instead of returning immediately: %s", elapsed)
	}
}
