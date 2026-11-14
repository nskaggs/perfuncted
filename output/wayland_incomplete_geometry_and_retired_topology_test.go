package output

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A compositor's initial output geometry is not a physical mode. Until the
// latter arrives, an output must not invent a capturable logical resolution.
func TestWaylandOutputKeepsUnknownDimensionsUntilValidPhysicalMode(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 13, Interface: "wl_output", Version: 4})
	out.baseX, out.baseY = -20, 35
	out.recomputeGeometryLocked()
	if out.info.Geometry.X != -20 || out.info.Geometry.Y != 35 || out.info.Geometry.W != 0 || out.info.Geometry.H != 0 {
		t.Fatalf("unmeasured output incorrectly advertised geometry: %+v", out.info.Geometry)
	}
	out.hasLogicalSize = true
	out.logicalW, out.logicalH = 1280, 720
	out.updateLogicalScaleLocked()
	if out.info.Scale != 1 || out.info.ScaleNumerator != 1 || out.info.ScaleDenominator != 1 {
		t.Fatalf("unknown physical mode supplied invented scale: %+v", out.info)
	}
	out.info.ResolutionW, out.info.ResolutionH = 1920, 1080
	out.recomputeGeometryLocked()
	if out.info.Geometry.W != 1280 || out.info.Geometry.H != 720 || out.info.Scale != 0 || out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 {
		t.Fatalf("valid physical mode did not establish fractional scale: %+v", out.info)
	}
}

// Once shutdown begins, topology reconciliation cannot accept freshly
// discovered outputs or overwrite the last known display collection.
func TestWaylandClosedListerRejectsTopologyReconciliationWithoutMutation(t *testing.T) {
	existing := newWaylandOutput(wl.GlobalEvent{Name: 8, Interface: "wl_output", Version: 4})
	existing.info.Name = "prior-private-display"
	l := &WaylandLister{outputs: []*waylandOutput{existing}}
	l.closed.Store(true)
	if err := l.reconcileTopology(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("retired lister accepted topology reconciliation: %v", err)
	}
	outputs := l.snapshotOutputs()
	if len(outputs) != 1 || outputs[0].Name != "prior-private-display" {
		t.Fatalf("closed lister lost its preexisting metadata: %+v", outputs)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

// No Wayland session is not an empty display topology: it is unavailable.
func TestWaylandUnavailableListerRejectsObservationAndClosesSafely(t *testing.T) {
	var missing *WaylandLister
	outputs, err := missing.List(context.Background())
	if err == nil || len(outputs) != 0 {
		t.Fatalf("nil lister reported outputs: %+v err=%v", outputs, err)
	}
	if err := missing.Close(); err != nil {
		t.Fatalf("closing absent lister: %v", err)
	}
	l := &WaylandLister{}
	outputs, err = l.List(context.Background())
	if err == nil || len(outputs) != 0 {
		t.Fatalf("uninitialized lister reported outputs: %+v err=%v", outputs, err)
	}
	if err := sendWaylandDestructor(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil output proxy required live connection: %v", err)
	}
}
