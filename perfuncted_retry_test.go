package perfuncted_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

func TestRetryNilContext(t *testing.T) {
	calls := 0
	//lint:ignore SA1012 regression test for nil-context handling
	if err := perfuncted.Retry(nil, 0, func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("Retry(nil): %v", err)
	}
	if calls != 1 {
		t.Fatalf("Retry(nil) calls = %d, want 1", calls)
	}
}

func TestRetryNilFunc(t *testing.T) {
	if err := perfuncted.Retry(context.Background(), 0, nil); err == nil {
		t.Fatal("Retry with nil function succeeded unexpectedly")
	}
}

func TestRetryReturnsLastErrorOnTimeout(t *testing.T) {
	boom := errors.New("boom")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := perfuncted.Retry(ctx, 0, func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Retry error = %v, want %v", err, boom)
	}
}
