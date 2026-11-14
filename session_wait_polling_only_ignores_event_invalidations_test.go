package perfuncted

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A polling-only predicate has no relationship to desktop event invalidations.
// Unrelated event storms must not bypass the configured polling interval or
// misattribute event wakeups to a state-only predicate.
func TestWaitPollingOnlyIgnoresUnrelatedInvalidationEvents(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	var evaluations atomic.Int32
	var ready atomic.Bool
	first := make(chan struct{})
	type result struct {
		evidence WaitEvidence
		err      error
	}
	completed := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		evidence, err := session.WaitWithEvidence(ctx, Predicate("external flag", func(context.Context) (bool, error) {
			if evaluations.Add(1) == 1 {
				close(first)
			}
			return ready.Load(), nil
		}), WaitEvery(250*time.Millisecond))
		completed <- result{evidence, err}
	}()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatalf("predicate did not start: %v", ctx.Err())
	}
	for i := 0; i < 20; i++ {
		session.notifyWaiters()
	}
	select {
	case <-time.After(20 * time.Millisecond):
	case got := <-completed:
		t.Fatalf("unrelated event ended polling-only wait: %+v", got)
	}
	if got := evaluations.Load(); got != 1 {
		t.Fatalf("unrelated desktop events triggered %d predicate evaluations, expected 1", got)
	}
	ready.Store(true)
	select {
	case got := <-completed:
		if got.err != nil || got.evidence.Evaluations < 2 || got.evidence.PollWakeups < 1 || got.evidence.Wakeups != 0 {
			t.Fatalf("poll-only evidence attributed desktop wakeups: %+v", got)
		}
	case <-ctx.Done():
		t.Fatalf("polling-only predicate never observed its new state: %v", ctx.Err())
	}
}
