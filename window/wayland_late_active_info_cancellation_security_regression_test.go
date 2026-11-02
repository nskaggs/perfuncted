package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A compositor may finish a round-trip just as the caller cancels. A window
// title or handle observed in that reply must not escape a canceled read.
type lateCancellationWaylandDisplay struct {
	*mockWaylandDisplay
	cancel context.CancelFunc
	calls  int
}

func (d *lateCancellationWaylandDisplay) RoundTripContext(context.Context) error {
	d.calls++
	d.cancel()
	return nil
}

func TestWaylandActiveTitleNeverDisclosesLateCancelledCompositorReply(t *testing.T) {
	wm, wire, id := newStubWaylandManager("private window", true, true)
	wm.toplevels[id].Active = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	title, err := wm.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("late canceled Wayland title escaped: title=%q err=%v", title, err)
	}
	if probe.calls != 1 {
		t.Fatalf("test did not reach compositor: %d", probe.calls)
	}
}

func TestWaylandInfoByIDNeverDisclosesLateCancelledCompositorReply(t *testing.T) {
	wm, wire, id := newStubWaylandManager("private window", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	info, err := wm.InfoByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || info.ID != 0 || info.Title != "" {
		t.Fatalf("late canceled Wayland info escaped: %+v err=%v", info, err)
	}
	if id != 50 || probe.calls != 1 {
		t.Fatalf("test did not reach expected compositor handle: id=%d calls=%d", id, probe.calls)
	}
}

func TestWaylandSyncMustNotAcknowledgeLateCanceledRoundTrip(t *testing.T) {
	wm, wire, _ := newStubWaylandManager("private window", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	if err := wm.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled Wayland Sync reported success: %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("did not exercise round trip: calls=%d", probe.calls)
	}
}

var _ wl.Ctx = (*mockWaylandContext)(nil)
