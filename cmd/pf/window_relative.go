package main

import (
	"fmt"
	"math"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/window"
)

// windowRelativePoint translates compositor-logical window client coordinates
// to the input backend's *declared* global space. Unknown coordinate spaces
// fail closed rather than silently clicking the wrong window.
func windowRelativePoint(w window.Info, x, y int, space input.CoordinateSpaceInfo) (int, int, error) {
	if err := space.Validate(); err != nil {
		return 0, 0, fmt.Errorf("window-relative input requires a declared coordinate space: %w", err)
	}
	if w.W <= 0 || w.H <= 0 || x < 0 || y < 0 || x >= w.W || y >= w.H {
		return 0, 0, fmt.Errorf("window-relative coordinates (%d,%d) outside window %dx%d: %w", x, y, w.W, w.H, window.ErrInvalidArgument)
	}
	px, py := float64(w.X)+float64(x), float64(w.Y)+float64(y)
	px *= space.ScaleX
	py *= space.ScaleY
	if math.IsInf(px, 0) || math.IsInf(py, 0) || px >= float64(math.MaxInt) || px <= float64(math.MinInt) || py >= float64(math.MaxInt) || py <= float64(math.MinInt) {
		return 0, 0, fmt.Errorf("window-relative point exceeds input coordinate range: %w", window.ErrInvalidArgument)
	}
	return int(math.Round(px)), int(math.Round(py)), nil
}
