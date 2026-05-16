package screen

import (
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestJanitorGoroutineExitsOnClose verifies that the janitor goroutine started
// by withWlrContext is terminated when Close is called.
func TestJanitorGoroutineExitsOnClose(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("fake-wl-sock-leak-check",
		func(sock string) (*wl.Context, error) { return &wl.Context{}, nil },
		time.Minute,
	)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("withWlrContext: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, b.ctx must be nil.
	b.ctxMu.Lock()
	ctxAfterClose := b.ctx
	b.ctxMu.Unlock()
	if ctxAfterClose != nil {
		t.Error("expected b.ctx == nil after Close")
	}
}

func BenchmarkWlrScreencopyBackendOpen(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		be := NewWlrScreencopyBackendWithConnector("bench-fake",
			func(sock string) (*wl.Context, error) { return &wl.Context{}, nil },
			5*time.Minute,
		)
		_ = be.withWlrContext(func(ctx *wl.Context) error { return nil })
		_ = be.Close()
	}
}
