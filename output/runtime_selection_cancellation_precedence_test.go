package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Model cancellation at the two distinct publication gates during backend
// selection. No X11 connection should be attempted after either boundary.
type cancelAfterOutputErrChecks struct {
	context.Context //nolint:containedctx // Synthetic context controls the exact cancellation boundary.
	count, cancelAt int
}

func (c *cancelAfterOutputErrChecks) Err() error {
	c.count++
	if c.count >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestOutputSelectionHonorsCancellationBeforeWaylandProbeAndX11Fallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cancelAt int
	}{
		{"before Wayland probe", 2},
		{"before X11 fallback", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cancelAfterOutputErrChecks{Context: context.Background(), cancelAt: tc.cancelAt}
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=nonexistent-output-socket",
				"XDG_RUNTIME_DIR=" + t.TempDir(),
				"DISPLAY=invalid-display-name",
			})
			selected, err := OpenRuntimeContext(ctx, rt)
			if selected != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation gate %d returned lister=%T err=%v", tc.cancelAt, selected, err)
			}
			if ctx.count != tc.cancelAt {
				t.Fatalf("checked cancellation %d times, want %d", ctx.count, tc.cancelAt)
			}
		})
	}
}
