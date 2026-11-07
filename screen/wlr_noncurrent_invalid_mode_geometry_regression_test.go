package screen

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.mode advertises available display modes, not only the active one.
// A non-current or invalid signed size must not replace the last confirmed
// screenshot geometry, since ResolutionWithContext can return these cached
// values without capturing another frame.
func TestWlrScreencopyIgnoresNoncurrentAndInvalidOutputModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flags, w, h uint32
	}{
		{"noncurrent alternate mode", 0, 1280, 720},
		{"preferred but not current", 2, 2560, 1440},
		{"negative width", wl.OutputModeCurrent, ^uint32(0), 1080},
		{"negative height", wl.OutputModeCurrent, 1920, ^uint32(0)},
		{"zero width", wl.OutputModeCurrent, 0, 1080},
		{"zero height", wl.OutputModeCurrent, 1920, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			send := func(flags, w, h uint32) {
				payload := make([]byte, 16)
				wl.PutUint32(payload[0:4], flags)
				wl.PutUint32(payload[4:8], w)
				wl.PutUint32(payload[8:12], h)
				wl.PutUint32(payload[12:16], 60000)
				b.applyOutputEvent(1, payload)
			}
			send(wl.OutputModeCurrent, 1920, 1080)
			send(tc.flags, tc.w, tc.h)
			if b.pW != 1920 || b.pH != 1080 {
				t.Fatalf("non-authoritative mode replaced current output geometry with %dx%d", b.pW, b.pH)
			}
		})
	}
}
