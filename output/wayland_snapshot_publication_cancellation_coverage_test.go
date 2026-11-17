package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type cancelOutputContextAfterErrCheck struct {
	context.Context
	cancel      context.CancelFunc
	cancelAfter int
	checks      int
}

func (c *cancelOutputContextAfterErrCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.cancelAfter {
		c.cancel()
	}
	return err
}

// Cancellation after synchronization and just before returning must not leak
// cached output metadata from the observation.
func TestWaylandListerWithholdsSnapshotWhenCanceledDuringPublication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancelAfter int
	}{
		{name: "after synchronization", cancelAfter: 2},
		{name: "between reconciliation and snapshot", cancelAfter: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelOutputContextAfterErrCheck{
				Context:     base,
				cancel:      cancel,
				cancelAfter: tc.cancelAfter,
			}
			lister := &WaylandLister{
				session: &wl.Session{},
				outputs: []*waylandOutput{{globalID: 7, info: Info{
					Name: "display-must-not-be-published", Backend: "wayland", Available: true,
				}}},
			}

			outputs, err := lister.List(ctx)
			if !errors.Is(err, context.Canceled) || outputs != nil {
				t.Fatalf("canceled observation returned outputs=%+v err=%v", outputs, err)
			}
			if ctx.checks != tc.cancelAfter+1 {
				t.Fatalf("cancellation check count=%d, want %d", ctx.checks, tc.cancelAfter+1)
			}
		})
	}
}
