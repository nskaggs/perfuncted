package screen

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Capture converts logical regions to physical pixels using wl_output.scale.
// Malformed zero or signed-negative compositor events must never replace a
// previously established scale and silently redirect the capture crop.
func TestExtCapturePreservesAuthorizedScaleAfterMalformedOutputEvent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint32
	}{
		{"zero", 0},
		{"negative one", ^uint32(0)},
		{"minimum signed", uint32(1) << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &ExtCaptureBackend{}
			initial := make([]byte, 4)
			wl.PutUint32(initial, 2)
			b.applyOutputEvent(wlOutputScaleOpcode, initial)
			if b.outputScale != 2 {
				t.Fatalf("initial scale=%d", b.outputScale)
			}
			malformed := make([]byte, 4)
			wl.PutUint32(malformed, tc.value)
			b.applyOutputEvent(wlOutputScaleOpcode, malformed)
			if b.outputScale != 2 {
				t.Fatalf("invalid %08x scale replaced confirmed value: %d", tc.value, b.outputScale)
			}
		})
	}
}
