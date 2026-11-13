package screen

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A revoked capture cannot open a new Wayland connection or run its callback,
// even if a connector would otherwise succeed. Health records the refusal.
func TestWlrCanceledOperationNeverConnectsOrExecutesCapture(t *testing.T) {
	connects, calls := 0, 0
	b := newWlrScreencopyBackendWithContextConnector("fake", func(context.Context, string) (*wl.Context, error) {
		connects++
		return &wl.Context{}, nil
	}, time.Minute)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.withWlrContextContext(ctx, func(*wl.Context, context.Context) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || connects != 0 || calls != 0 {
		t.Fatalf("canceled capture crossed transport boundary: err=%v connects=%d callbacks=%d", err, connects, calls)
	}
	if got := strings.Join(b.Diagnostics(), "; "); !strings.Contains(got, "context canceled") || !strings.Contains(got, "reconnect required") {
		t.Fatalf("canceled operation not reflected in backend diagnostics: %s", got)
	}
}

// Close owns the current capture's cancel function even when no connection
// was yet installed. Cancellation must happen once and stay idempotent.
func TestWlrCloseCancelsActiveCaptureAndDoesNotReviveIt(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("fake", nil, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.activeCancel = cancel
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("close did not cancel active capture: %v", ctx.Err())
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed capture backend accepted another operation: %v", err)
	}
}
