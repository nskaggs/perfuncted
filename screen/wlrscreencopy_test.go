package screen

import (
	"fmt"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrResolutionReturnsPhysicalDimensions(t *testing.T) {
	b := &WlrScreencopyBackend{pW: 1920, pH: 1080, scale: 2}
	w, h, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution() error = %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Fatalf("Resolution() = %dx%d, want 1920x1080", w, h)
	}
}

func TestWlrResolutionIgnoresScaleOneAndZero(t *testing.T) {
	tests := []struct {
		name  string
		scale uint32
	}{
		{name: "scale zero", scale: 0},
		{name: "scale one", scale: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{pW: 1366, pH: 768, scale: tt.scale}
			w, h, err := b.Resolution()
			if err != nil {
				t.Fatalf("Resolution() error = %v", err)
			}
			if w != 1366 || h != 768 {
				t.Fatalf("Resolution() = %dx%d, want 1366x768", w, h)
			}
		})
	}
}

func TestWithWlrContextCachingAndReset(t *testing.T) {
	// Create backend with fake connector
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock", func(sock string) (*wl.Context, error) { return &wl.Context{}, nil }, 5*time.Minute)
	defer b.Close()
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
	defer b.Close()
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

func TestWlrScreencopyBackendCloseIsIdempotent(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("/tmp/fake-wl-sock-close", func(sock string) (*wl.Context, error) {
		return &wl.Context{}, nil
	}, time.Minute)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("setup withWlrContext failed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}
