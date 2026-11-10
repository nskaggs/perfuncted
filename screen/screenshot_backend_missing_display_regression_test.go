package screen

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Selecting X11 must prove that the supplied display endpoint is usable.
// An explicit but nonexistent display must not publish a screenshot backend.
func TestScreenOpenRuntimeRejectsUnreachableExplicitX11Display(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:999999", "XDG_SESSION_TYPE=x11"})
	b, err := OpenRuntimeContext(context.Background(), rt)
	if b != nil || err == nil {
		t.Fatalf("unreachable X11 display published screenshot backend=%T err=%v", b, err)
	}
}

// Cancellation takes precedence over backend selection even with no usable
// compositor endpoint; never turn cancellation into an availability fallback.
func TestScreenOpenRuntimeCanceledBeforeCompositorDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt := env.FromEnviron([]string{"DISPLAY=:999999", "XDG_SESSION_TYPE=x11"})
	b, err := OpenRuntimeContext(ctx, rt)
	if b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery returned backend=%T err=%v", b, err)
	}
	if err != nil && strings.Contains(err.Error(), "dial") {
		t.Fatalf("canceled discovery attempted X11 dial: %v", err)
	}
}
