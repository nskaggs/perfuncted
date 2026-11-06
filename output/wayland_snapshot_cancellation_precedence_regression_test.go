package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// An already-ended observation is not permitted to return a cached output
// snapshot, even when the lister still has perfectly valid old metadata.
func TestWaylandListerCanceledObservationWithholdsCachedOutputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := &WaylandLister{session: &wl.Session{}, outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "previous-private-display", Backend: "wayland", Available: true}},
	}}
	outputs, err := l.List(ctx)
	if outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller exposed output metadata: %+v %v", outputs, err)
	}
}

// The lister's own stop signal also invalidates snapshots, independently of
// the observing caller's context. This models a session loss while clients
// retain their reference to an otherwise initialized output lister.
func TestWaylandListerLifecycleStopWithholdsCachedOutputs(t *testing.T) {
	stopCtx, stop := context.WithCancel(context.Background())
	stop()
	l := &WaylandLister{session: &wl.Session{}, stopCtx: stopCtx, outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "stale-output", Backend: "wayland", Available: true}},
	}}
	outputs, err := l.List(context.Background())
	if outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped output lifecycle returned %+v %v", outputs, err)
	}
}
