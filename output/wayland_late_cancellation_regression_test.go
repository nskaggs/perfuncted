package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Cancel at the sync/reconciliation boundary, after the initial admission
// check but before the output snapshot is published to the caller.
type cancelOutputContextAfterAdmission struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOutputContextAfterAdmission) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == 1 {
		c.cancel()
	}
	return err
}

func TestWaylandListerRejectsCancellationAfterSessionSynchronization(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOutputContextAfterAdmission{Context: base, cancel: cancel}
	l := &WaylandLister{
		session: &wl.Session{},
		outputs: []*waylandOutput{{globalID: 7, info: Info{Name: "private-display", Available: true}}},
	}
	outputs, err := l.List(ctx)
	if !errors.Is(err, context.Canceled) || len(outputs) != 0 {
		t.Fatalf("canceled output enumeration exposed outputs=%v err=%v", outputs, err)
	}
	if ctx.checks < 1 {
		t.Fatalf("cancellation boundary not reached: %d checks", ctx.checks)
	}
}
