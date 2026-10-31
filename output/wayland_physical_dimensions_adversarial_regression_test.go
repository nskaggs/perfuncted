package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.geometry's physical_width and physical_height are signed int32
// millimeters. A negative value is an unavailable measurement, not an
// advertised negative physical panel size.
func TestWaylandGeometryTreatsNegativePhysicalMillimetersAsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int32
		wantW, wantH  int
	}{
		{"unknown width", -1, 335, 0, 335},
		{"unknown height", 590, -1, 590, 0},
		{"both invalid", -2147483648, -9, 0, 0},
		{"valid dimensions", 600, 340, 600, 340},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 12, Interface: "wl_output", Version: 4})
			proxy := &wl.RawProxy{}
			out.updateProxy(proxy)
			wire := make([]byte, 20)
			wl.PutUint32(wire[8:12], uint32(tc.width))
			wl.PutUint32(wire[12:16], uint32(tc.height))
			proxy.OnEvent(0, 0, wire)
			if out.info.PhysicalW != tc.wantW || out.info.PhysicalH != tc.wantH {
				t.Fatalf("compositor physical millimeters (%d,%d) became (%d,%d); want (%d,%d)", tc.width, tc.height, out.info.PhysicalW, out.info.PhysicalH, tc.wantW, tc.wantH)
			}
		})
	}
}
