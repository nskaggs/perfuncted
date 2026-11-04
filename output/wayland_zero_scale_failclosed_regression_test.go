package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.scale is a positive integer. A malformed zero event must not
// erase previously confirmed geometry when the compositor is reconfiguring.
func TestWaylandOutputRejectsZeroScaleWithoutLosingConfirmedGeometry(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 25, Interface: "wl_output", Version: 4})
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 2560)
	wl.PutUint32(mode[8:12], 1440)
	proxy.OnEvent(1, 0, mode)
	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	proxy.OnEvent(3, 0, scale)
	before := out.info
	wl.PutUint32(scale, 0)
	proxy.OnEvent(3, 0, scale)
	after := out.info
	if after.Scale != before.Scale || after.ScaleNumerator != before.ScaleNumerator ||
		after.ScaleDenominator != before.ScaleDenominator || after.Geometry != before.Geometry {
		t.Fatalf("malformed zero scale replaced authoritative output geometry: before=%+v after=%+v", before, after)
	}
}
