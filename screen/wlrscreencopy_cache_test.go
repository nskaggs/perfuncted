package screen

import (
	"fmt"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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
	defer b.Close()

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
