package poll

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var errNotReady = errors.New("not ready")

func TestWaitFuncReturnsValue(t *testing.T) {
	got, err := WaitFunc(context.Background(), time.Millisecond, func() (string, error) {
		return "ready", nil
	})
	if err != nil {
		t.Fatalf("WaitFunc: %v", err)
	}
	if got != "ready" {
		t.Fatalf("WaitFunc result = %q, want %q", got, "ready")
	}
}

func TestWaitFuncRetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	got, err := WaitFunc(context.Background(), time.Millisecond, func() (int, error) {
		if calls.Add(1) < 3 {
			return 0, errNotReady
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("WaitFunc: %v", err)
	}
	if got != 42 {
		t.Fatalf("WaitFunc result = %d, want 42", got)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestWaitFuncNilContext(t *testing.T) {
	var nilCtx context.Context
	got, err := WaitFunc(nilCtx, 0, func() (string, error) {
		return "defaulted", nil
	})
	if err != nil {
		t.Fatalf("WaitFunc(nil ctx): %v", err)
	}
	if got != "defaulted" {
		t.Fatalf("WaitFunc result = %q, want defaulted", got)
	}
}

func TestWaitFuncNilFunc(t *testing.T) {
	if _, err := WaitFunc[string](context.Background(), 0, nil); err == nil {
		t.Fatal("WaitFunc with nil function returned nil error")
	}
}

func TestWaitFuncCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := WaitFunc(ctx, time.Millisecond, func() (string, error) {
		return "", errNotReady
	}); err == nil {
		t.Fatal("WaitFunc on canceled context returned nil error")
	}
}
