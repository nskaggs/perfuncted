package perfuncted_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

var errRetryPending = errors.New("retry pending")

func TestRetry_SuccessOnFirstCall(t *testing.T) {
	t.Parallel()
	calls := 0
	err := perfuncted.Retry(context.Background(), 10*time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetry_SuccessAfterRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	err := perfuncted.Retry(context.Background(), time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errRetryPending
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetry_ZeroPollDefaultsTo10ms(t *testing.T) {
	t.Parallel()
	// Zero poll should default to 10ms and not cause a panic/division by zero
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	err := perfuncted.Retry(ctx, 0, func() error {
		calls++
		return errRetryPending
	})
	if err == nil {
		t.Fatal("expected error from timed-out retry")
	}
	if calls == 0 {
		t.Fatal("expected at least one call")
	}
}
