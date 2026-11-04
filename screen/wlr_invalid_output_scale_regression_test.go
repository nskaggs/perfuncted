package screen

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A compositor's malformed signed scale update must not change a known
// logical-to-physical coordinate mapping for subsequent screen captures.
func TestWlrScreencopyRejectsInvalidScaleAfterValidOutputGeometry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale uint32
	}{
		{"zero", 0},
		{"negative one", ^uint32(0)},
		{"minimum signed", 1 << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			valid := make([]byte, 4)
			wl.PutUint32(valid, 2)
			b.applyOutputEvent(3, valid)
			malformed := make([]byte, 4)
			wl.PutUint32(malformed, tc.scale)
			b.applyOutputEvent(3, malformed)
			if b.scale != 2 {
				t.Fatalf("invalid scale %08x changed established scale to %d", tc.scale, b.scale)
			}
		})
	}
}
