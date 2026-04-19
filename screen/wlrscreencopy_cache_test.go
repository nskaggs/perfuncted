package screen

import (
	"fmt"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// TestGlobalWlrCacheLimit verifies that the global wlr cache does not exceed the configured cap.
func TestGlobalWlrCacheLimit(t *testing.T) {
	orig := maxWlrCachedContexts
	maxWlrCachedContexts = 3
	defer func() { maxWlrCachedContexts = orig }()

	for i := 0; i < 6; i++ {
		b := NewWlrScreencopyBackendWithConnector("/tmp/fake", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 50*time.Millisecond)
		if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
			t.Fatalf("withWlrContext failed: %v", err)
		}
	}

	// wait for janitors to trim
	time.Sleep(200 * time.Millisecond)
	globalWlrMu.Lock()
	n := len(globalWlrCtxs)
	globalWlrMu.Unlock()
	if n > maxWlrCachedContexts {
		t.Fatalf("global cache size %d exceeds cap %d", n, maxWlrCachedContexts)
	}
}

// TestWithWlrContextReconnect simulates a protocol error on the first context
// and ensures withWlrContext resets the cached ctx and reconnects on next call.
func TestWithWlrContextReconnect(t *testing.T) {
	call := 0

	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-reconnect", func(sock string) (*wl.Context, error) {
		// first call returns a context that will cause fn to return error
		if call == 0 {
			call++
			return &wl.Context{}, nil
		}
		// subsequent calls succeed
		call++
		return &wl.Context{}, nil
	}, 5*time.Minute)

	// First withWlrContext: simulate fn returning an error (protocol error)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return fmt.Errorf("simulated protocol error") }); err == nil {
		t.Fatalf("expected simulated protocol error")
	}

	// Ensure ctx was reset
	if b.ctx != nil {
		t.Fatalf("expected cached ctx to be nil after error")
	}

	// Next call should reconnect
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("expected reconnect to succeed, got %v", err)
	}
}
