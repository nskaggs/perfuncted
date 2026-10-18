package find

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Immediate fixed-interval completion must not set up a ticker that will
// never be read. This is the common early-exit path after screen hash checks.
func TestFixedPollImmediateDoesNotAllocateTicker(t *testing.T) {
	ctx := context.Background()
	immediate := func(int) (bool, uint32, error) { return true, 0x12345678, nil }
	allocs := testing.AllocsPerRun(100, func() {
		got, err := poll(ctx, time.Millisecond, 0, immediate)
		if err != nil || got != 0x12345678 {
			t.Fatalf("poll got=%08x err=%v", got, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("immediate polling allocs=%g; a ticker should not be allocated", allocs)
	}
}

func TestFixedPollDelayedObservationAndCancellation(t *testing.T) {
	t.Run("delayed result", func(t *testing.T) {
		attempts := 0
		got, err := poll(context.Background(), time.Microsecond, 0, func(int) (bool, uint32, error) {
			attempts++
			return attempts == 3, uint32(attempts), nil
		})
		if err != nil || got != 3 || attempts != 3 {
			t.Fatalf("poll got=%d attempts=%d err=%v", got, attempts, err)
		}
	})
	t.Run("cancellation after first observation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		got, err := poll(ctx, time.Second, 9, func(int) (bool, uint32, error) {
			attempts++
			cancel()
			return false, 27, nil
		})
		if !errors.Is(err, context.Canceled) || got != 27 || attempts != 1 {
			t.Fatalf("poll got=%d attempts=%d err=%v", got, attempts, err)
		}
	})
}
