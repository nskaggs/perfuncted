package screen

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Queued initialization can sit behind a prior capture's backend lock.
// Neither cancellation nor shutdown while queued may connect a new Wayland
// socket or allow an operation on already-retired authorization.
func TestWlrQueuedConnectionCancelsBeforeConnectorCall(t *testing.T) {
	var connectCalls atomic.Int32
	backend := NewWlrScreencopyBackendWithConnector("unused", func(string) (*wl.Context, error) { connectCalls.Add(1); return &wl.Context{}, nil }, time.Minute)
	defer backend.Close()
	entered := make(chan struct{})
	backend.initJanitor = func() { close(entered) }
	backend.ctxMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- backend.withWlrContextContext(ctx, func(*wl.Context, context.Context) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		cancel()
		t.Fatal("operation did not reach queued initialization")
	}
	cancel()
	backend.ctxMu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled queued connection returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued connection did not finish")
	}
	if calls := connectCalls.Load(); calls != 0 {
		t.Fatalf("canceled operation invoked connector %d times", calls)
	}
	backend.ctxMu.Lock()
	hasConnection := backend.ctx != nil
	backend.ctxMu.Unlock()
	if hasConnection {
		t.Fatal("canceled operation cached an unauthorized connection")
	}
}

func TestWlrCloseDuringQueuedConnectionPreventsNewTransport(t *testing.T) {
	var connectCalls atomic.Int32
	backend := NewWlrScreencopyBackendWithConnector("unused", func(string) (*wl.Context, error) { connectCalls.Add(1); return &wl.Context{}, nil }, time.Minute)
	entered := make(chan struct{})
	backend.initJanitor = func() { close(entered) }
	backend.ctxMu.Lock()
	pending := make(chan error, 1)
	go func() {
		pending <- backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		t.Fatal("operation did not reach queued initialization")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- backend.Close() }()
	select {
	case <-backend.done:
	case <-time.After(time.Second):
		backend.ctxMu.Unlock()
		t.Fatal("Close did not retire backend")
	}
	backend.ctxMu.Unlock()
	select {
	case err := <-pending:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("queued call survived backend Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued call never returned")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not release queued call")
	}
	if calls := connectCalls.Load(); calls != 0 {
		t.Fatalf("closed backend invoked connector %d times", calls)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
