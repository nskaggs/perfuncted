package operation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestTrackerCloseCancelsAndDrainsBeforeRelease(t *testing.T) {
	var owner Tracker
	ctx, finish, err := owner.Begin(context.Background(), errors.New("closed"))
	if err != nil {
		t.Fatal(err)
	}
	const users = 4
	failed := errors.New("release failed")
	var calls atomic.Int32
	releasing := make(chan struct{})
	allowRelease := make(chan struct{})
	results := make(chan error, users)
	var wg sync.WaitGroup
	for range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- owner.Close(func() error {
				calls.Add(1)
				close(releasing)
				<-allowRelease
				return failed
			})
		}()
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel admitted operation")
	}
	if !owner.Closed() {
		t.Fatal("Close did not reject admission while operations were active")
	}
	if _, _, err := owner.Begin(context.Background(), failed); !errors.Is(err, failed) {
		t.Fatalf("late admission returned %v", err)
	}
	select {
	case <-releasing:
		t.Fatal("release ran before the admitted operation finished")
	default:
	}
	finish()
	finish() // releases must be safe to invoke redundantly
	select {
	case <-releasing:
	case <-time.After(time.Second):
		t.Fatal("resource release did not begin after operations drained")
	}
	close(allowRelease)
	wg.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, failed) {
			t.Errorf("concurrent Close result %v lost shared resource error", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("release called %d times; want 1", got)
	}
}

func TestTrackerCanceledBeginAndEmptyClose(t *testing.T) {
	var owner Tracker
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := owner.Begin(ctx, errors.New("closed")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission: %v", err)
	}
	if err := owner.Close(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(func() error { t.Fatal("double release"); return nil }); err != nil {
		t.Fatal(err)
	}
}
