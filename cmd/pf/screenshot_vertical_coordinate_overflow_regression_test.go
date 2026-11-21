package main

import (
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/output"
)

// The vertical axis must reject a logical coordinate beyond the platform int
// range even when the horizontal axis maps correctly. Returning either partial
// coordinate could send an input event to an unrelated window.
func TestScreenshotLogicalVerticalOverflowRejectsEntirePoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin int
		pixel  int
	}{
		{name: "positive overflow", origin: math.MaxInt, pixel: 1},
		{name: "negative underflow", origin: math.MinInt, pixel: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := []output.Info{{Available: true, Geometry: output.Geometry{X: 100, Y: tc.origin, W: 3, H: 3}, ResolutionW: 3, ResolutionH: 3}}
			x, y, err := screenshotPointToLogical(info, 1, tc.pixel)
			if tc.origin == math.MinInt {
				// Pixel 0 is exactly the valid minimum coordinate.
				if err != nil || x != 101 || y != math.MinInt {
					t.Fatalf("minimum valid origin: (%d,%d), err=%v", x, y, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "out of range") || x != 0 || y != 0 {
				t.Fatalf("vertical overflow produced (%d,%d), err=%v; must reject whole point", x, y, err)
			}
		})
	}
}
