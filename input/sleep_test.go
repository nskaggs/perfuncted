package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A wait that runs past the caller's deadline must report the deadline even
// though the raw timer and the context's own deadline callback can both be ready,
// in which case select may pick either.
func TestSleepContextReportsElapsedDeadline(t *testing.T) {
	for range 50 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		err := sleepContext(ctx, 30*time.Millisecond)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("sleepContext = %v, want context.DeadlineExceeded", err)
		}
	}
}

// A wait that finishes comfortably before the deadline is a success.
func TestSleepContextSucceedsWithinDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := sleepContext(ctx, time.Millisecond); err != nil {
		t.Fatalf("sleepContext = %v, want nil", err)
	}
}

// Cancellation is still reported as cancellation, not as a deadline.
func TestSleepContextReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := sleepContext(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext = %v, want context.Canceled", err)
	}
}

// A zero or negative wait reports the deadline when one has already passed.
func TestSleepContextZeroDurationReportsElapsedDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	if err := sleepContext(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleepContext(0) = %v, want context.DeadlineExceeded", err)
	}
}

// A click whose hold outlives the caller's deadline must not report success.
// Which touchpad calls were made before the deadline landed is not asserted
// here: a deadline that expires first legitimately shortens the sequence. The
// event order for a click that gets that far is covered separately.
func TestUinputMouseClickReportsElapsedDeadline(t *testing.T) {
	for range 50 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		tp := &recordingTouchPad{}
		b := &UinputBackend{touchpad: tp}
		err := b.MouseClick(ctx, 3, 4, 1)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("MouseClick = %v, want context.DeadlineExceeded", err)
		}
	}
}
