package input

import (
	"context"
	"testing"
	"time"
)

func TestSleepContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleepContext(ctx, 10*time.Millisecond); err != context.Canceled {
		t.Fatalf("sleepContext returned %v, want context.Canceled", err)
	}
}

func TestSleepContextCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := sleepContext(ctx, 1*time.Millisecond); err != nil {
		t.Fatalf("sleepContext returned %v, want nil", err)
	}
}
