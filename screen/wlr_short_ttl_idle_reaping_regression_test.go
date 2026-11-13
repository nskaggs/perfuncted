package screen

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Cache TTLs shorter than the ticker's minimum supported interval must not
// strand idle Wayland resources or continuously spin. After the idle context
// is reaped, the next authorized operation must create a fresh connection.
func TestWlrSubmillisecondCacheTTLReapsIdleContextAndReconnects(t *testing.T) {
	var connects atomic.Int32
	backend := newWlrScreencopyBackendWithContextConnector("synthetic-short-ttl", func(context.Context, string) (*wl.Context, error) {
		connects.Add(1)
		return &wl.Context{}, nil
	}, time.Nanosecond)
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err != nil {
		t.Fatalf("initial capture connection: %v", err)
	}
	if connects.Load() != 1 {
		t.Fatalf("unexpected initial connections: %d", connects.Load())
	}
	// The janitor owns ctxMu; inspect under the same lock and bound the wait.
	deadline := time.Now().Add(2 * time.Second)
	for {
		backend.ctxMu.Lock()
		idle := backend.ctx == nil
		backend.ctxMu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("submillisecond TTL never released idle Wayland context")
		}
		time.Sleep(time.Millisecond)
	}
	if err := backend.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err != nil {
		t.Fatalf("post-reap reconnection: %v", err)
	}
	if connects.Load() != 2 {
		t.Fatalf("cached expired context reused: connectors=%d, want two", connects.Load())
	}
}
