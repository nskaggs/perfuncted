package gnomebridge

import (
	"image"
	"strings"
	"testing"
)

// GNOME's D-Bus capture API expects signed 32-bit coordinates and dimensions.
// Values outside that domain must be rejected before an integer cast can wrap
// them to an unrelated screen region or authorize an unexpected capture.
func TestGnomeCaptureRegionRefusesOverflowedLogicalCoordinates(t *testing.T) {
	const maxI32 = int64(1<<31 - 1)
	const minI32 = -maxI32 - 1
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"positive X overflow", image.Rect(int(maxI32+1), 0, int(maxI32+2), 1)},
		{"negative X overflow", image.Rect(int(minI32-1), 0, int(minI32), 1)},
		{"positive Y overflow", image.Rect(0, int(maxI32+1), 1, int(maxI32+2))},
		{"negative Y overflow", image.Rect(0, int(minI32-1), 1, int(minI32))},
		{"width overflow", image.Rect(int(minI32), 0, int(maxI32), 1)},
		{"height overflow", image.Rect(0, int(minI32), 1, int(maxI32))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{}
			_, err := client.CaptureRegion(nil, 7, tc.rect)
			if err == nil || !strings.Contains(err.Error(), "range") {
				t.Fatalf("oversized region %+v did not fail coordinate range validation: %v", tc.rect, err)
			}
		})
	}
}
