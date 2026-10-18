package operation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// A deterministic synthetic burst covers different numbers of admitted
// operations, mixed early releases, concurrent Close callers, and a terminal
// release failure without starting any compositor, network, or device.
func TestSyntheticTrackerShutdownMatrix(t *testing.T) {
	closedErr := errors.New("synthetic backend closed")
	teardownErr := errors.New("synthetic teardown failed")
	for _, capacity := range []int{1, 2, 4, 8, 16} {
		for round := range 24 {
			var tracker Tracker
			contexts := make([]context.Context, 0, capacity)
			finishes := make([]func(), 0, capacity)
			for i := range capacity {
				ctx, finish, err := tracker.Begin(context.Background(), closedErr)
				if err != nil {
					t.Fatalf("round %d, admitted %d: %v", round, i, err)
				}
				contexts = append(contexts, ctx)
				finishes = append(finishes, finish)
			}
			// Some releases finish before Close, some after it starts.
			for i, finish := range finishes {
				if (i+round)%3 == 0 {
					finish()
				}
			}
			var teardownCalls atomic.Int32
			const closers = 4
			results := make(chan error, closers)
			var wg sync.WaitGroup
			for range closers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- tracker.Close(func() error {
						teardownCalls.Add(1)
						return teardownErr
					})
				}()
			}
			for i, finish := range finishes {
				if (i+round)%3 != 0 {
					<-contexts[i].Done()
					finish()
				}
			}
			wg.Wait()
			close(results)
			for err := range results {
				if !errors.Is(err, teardownErr) {
					t.Fatalf("round %d: Close lost teardown error: %v", round, err)
				}
			}
			if teardownCalls.Load() != 1 {
				t.Fatalf("round %d: teardown calls=%d", round, teardownCalls.Load())
			}
			if _, _, err := tracker.Begin(context.Background(), closedErr); !errors.Is(err, closedErr) {
				t.Fatalf("round %d: admission after Close: %v", round, err)
			}
		}
	}
}

func BenchmarkSyntheticTrackerAdmission(b *testing.B) {
	var tracker Tracker
	ctx := context.Background()
	closedErr := errors.New("closed")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, finish, err := tracker.Begin(ctx, closedErr)
		if err != nil {
			b.Fatal(err)
		}
		finish()
	}
}
