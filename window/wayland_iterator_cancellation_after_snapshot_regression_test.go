package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

// A compositor may return multiple windows while the caller cancels after
// processing one. The remaining snapshot must not be exposed as live state.
func TestWaylandIteratorCancellationAfterFirstWindowFailsClosed(t *testing.T) {
	mgr, _, _ := newStubWaylandManager("first", true, false)
	mgr.toplevels[51] = &Info{ID: 51, Title: "second"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := 0
	gotCancellation := false
	for win, err := range mgr.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if win.ID != 0 {
				t.Fatalf("canceled iteration exposed window %#v", win)
			}
			gotCancellation = true
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen++
		if seen > 1 {
			t.Fatalf("iterator leaked %d windows after cancellation", seen)
		}
		cancel()
	}
	if seen != 1 || !gotCancellation {
		t.Fatalf("got %d windows and cancellation=%v, want one then cancellation", seen, gotCancellation)
	}
}

// Cancellation during the sync boundary must be checked before publishing
// compositor data, not only before issuing the round-trip.
func TestWaylandIteratorCancellationDuringRoundTripDoesNotPublishWindows(t *testing.T) {
	mgr, _, _ := newStubWaylandManager("private-window", true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// RoundTripContext returns nil after cancel to simulate a transport that
	// completes at the same instant the request is revoked.
	mgr.display = &cancelAfterSuccessfulWindowRoundTrip{display: mgr.display, cancel: cancel}
	seen := 0
	for win, err := range mgr.IterateWindows(ctx) {
		seen++
		if !errors.Is(err, context.Canceled) || win.ID != 0 {
			t.Fatalf("canceled round-trip exposed %#v, %v", win, err)
		}
	}
	if seen != 1 {
		t.Fatalf("got %d results, want one cancellation error", seen)
	}
}

type cancelAfterSuccessfulWindowRoundTrip struct {
	display interface {
		Context() wl.Ctx
		GetRegistry() (*wl.Registry, error)
		RoundTripContext(context.Context) error
	}
	cancel context.CancelFunc
}

func (d *cancelAfterSuccessfulWindowRoundTrip) Context() wl.Ctx { return d.display.Context() }
func (d *cancelAfterSuccessfulWindowRoundTrip) GetRegistry() (*wl.Registry, error) {
	return d.display.GetRegistry()
}
func (d *cancelAfterSuccessfulWindowRoundTrip) RoundTripContext(ctx context.Context) error {
	if err := d.display.RoundTripContext(ctx); err != nil {
		return err
	}
	d.cancel()
	return nil
}
