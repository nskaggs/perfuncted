package poll

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdaptivePollCapsBeforeMultiplyingPastMaximum(t *testing.T) {
	for _, tc := range []struct {
		attempt   int
		base, max time.Duration
	}{
		{attempt: 1, base: 120 * time.Millisecond, max: 100 * time.Millisecond},
		{attempt: 1, base: 70 * time.Millisecond, max: 100 * time.Millisecond},
		{attempt: 20, base: 70 * time.Millisecond, max: 100 * time.Millisecond},
		{attempt: 80, base: time.Nanosecond, max: time.Duration(1<<63 - 1)},
	} {
		got := AdaptivePoll(tc.attempt, tc.base, tc.max)
		if got > tc.max || got <= 0 {
			t.Fatalf("AdaptivePoll(%d,%s,%s)=%s", tc.attempt, tc.base, tc.max, got)
		}
		if tc.base == 70*time.Millisecond && got != tc.max {
			t.Fatalf("expected saturation at %s, got %s", tc.max, got)
		}
	}
}

func TestWaitFuncCancellationDuringRetryWaitDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan struct{})
	result := make(chan error, 1)
	calls := make(chan struct{}, 2)
	go func() {
		_, err := WaitFunc(ctx, time.Hour, func() (string, error) { calls <- struct{}{}; close(observed); return "", errNotReady })
		result <- err
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("first retry not attempted")
	}
	// A long retry interval means the caller must return because of context
	// cancellation, not a timer and not a second poll evaluation.
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled polling blocked")
	}
	if len(calls) != 1 {
		t.Fatalf("retry after cancellation: %d evaluations", len(calls))
	}
}
