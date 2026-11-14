package screen

import (
	"image"
	"testing"
)

// Empty compositor transfers and absent geometry cannot become valid captures,
// even when the surrounding transport otherwise reports successful delivery.
func TestRawPixelDecodersRejectUnavailableFramesBeforeAllocation(t *testing.T) {
	cases := []struct {
		name                  string
		pixels                []byte
		width, height, stride int
	}{
		{"empty transport", nil, 1, 1, 4},
		{"zero width", []byte{1, 2, 3, 4}, 0, 1, 4},
		{"zero height", []byte{1, 2, 3, 4}, 1, 0, 4},
		{"negative stride", []byte{1, 2, 3, 4}, 1, 1, -4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := decodeBGRA(tc.pixels, tc.width, tc.height, tc.stride)
			region := decodeBGRARect(tc.pixels, tc.width, tc.height, tc.stride, image.Rect(0, 0, 1, 1))
			if !full.Bounds().Empty() || !region.Bounds().Empty() {
				t.Fatalf("unavailable frame was decoded: full=%v region=%v", full.Bounds(), region.Bounds())
			}
		})
	}
}
