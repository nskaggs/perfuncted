package operation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
