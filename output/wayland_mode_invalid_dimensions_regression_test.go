package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.mode is controlled by the compositor. A malformed live mode
// update must not replace the last verified geometry with impossible bounds.
func TestWaylandOutputIgnoresInvalidCurrentModeDimensions(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 11, Interface: "wl_output", Version: 4})
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 1920)
	wl.PutUint32(mode[8:12], 1080)
	wl.PutUint32(mode[12:16], 60000)
	proxy.OnEvent(1, 0, mode)
	if out.info.ResolutionW != 1920 || out.info.ResolutionH != 1080 || out.info.Geometry.W != 1920 {
		t.Fatalf("valid physical mode not initialized %+v", out.info)
	}
	for _, tc := range []struct {
		name          string
		width, height uint32
	}{
		{"negative width", ^uint32(0), 1080},
		{"negative height", 1920, ^uint32(0)},
		{"zero width", 0, 1080},
		{"zero height", 1920, 0},
		{"both invalid", ^uint32(0), ^uint32(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wl.PutUint32(mode[4:8], tc.width)
			wl.PutUint32(mode[8:12], tc.height)
			proxy.OnEvent(1, 0, mode)
			if out.info.ResolutionW != 1920 || out.info.ResolutionH != 1080 || out.info.Geometry.W != 1920 || out.info.Geometry.H != 1080 {
				t.Fatalf("malformed current mode replaced valid resolution: %+v", out.info)
			}
		})
	}
}
