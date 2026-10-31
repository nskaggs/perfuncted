package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.scale is a signed protocol int32. Negative wire values are not
// legitimate 4-billion-fold scales and must not corrupt visible geometry.
func TestWaylandSignedOutputScaleRejectsNegativeProtocolValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint32
	}{
		{"minus one", ^uint32(0)},
		{"minimum signed", uint32(1) << 31},
		{"minus two", ^uint32(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 4})
			proxy := &wl.RawProxy{}
			out.updateProxy(proxy)
			mode := make([]byte, 16)
			wl.PutUint32(mode[0:4], wl.OutputModeCurrent)
			wl.PutUint32(mode[4:8], 1920)
			wl.PutUint32(mode[8:12], 1080)
			proxy.OnEvent(1, 0, mode)
			scale := make([]byte, 4)
			wl.PutUint32(scale, 2)
			proxy.OnEvent(3, 0, scale)
			wl.PutUint32(scale, tc.value)
			proxy.OnEvent(3, 0, scale)
			got := out.info
			if got.Scale != 2 || got.ScaleNumerator != 2 || got.ScaleDenominator != 1 || got.Geometry.W != 960 || got.Geometry.H != 540 {
				t.Fatalf("negative signed scale %08x corrupted live output: %+v", tc.value, got)
			}
		})
	}
}
