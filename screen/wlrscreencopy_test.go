package screen

import (
	"fmt"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWithWlrContextCachingAndReset(t *testing.T) {
	// Create backend with fake connector
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 5*time.Minute)
	// first call should create a context
	var firstPtr, secondPtr *wl.Context
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		firstPtr = ctx
		return nil
	}); err != nil {
		t.Fatalf("first withWlrContext failed: %v", err)
	}

	// second call should reuse same pointer
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		secondPtr = ctx
		return nil
	}); err != nil {
		t.Fatalf("second withWlrContext failed: %v", err)
	}

	if firstPtr != secondPtr {
		t.Fatalf("expected same ctx pointer, got different: %p vs %p", firstPtr, secondPtr)
	}

	// simulate failure during fn; cached context should be closed and reset
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		return fmt.Errorf("simulated")
	}); err == nil {
		t.Fatalf("expected error from simulated fn")
	}

	if b.ctx != nil {
		t.Fatalf("expected cached ctx to be nil after error, got %v", b.ctx)
	}
}

func TestWlrCacheJanitorEvicts(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock-evict", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 50*time.Millisecond)
	// create context
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("setup withWlrContext failed: %v", err)
	}

	// mark lastUsed as old
	b.ctxMu.Lock()
	if b.ctx != nil {
		b.lastUsed = time.Now().Add(-time.Hour)
	}
	b.ctxMu.Unlock()

	// wait for janitor to run
	time.Sleep(150 * time.Millisecond)

	b.ctxMu.Lock()
	exists := b.ctx != nil
	b.ctxMu.Unlock()
	if exists {
		t.Fatalf("expected cache entry to be evicted by janitor")
	}
}
