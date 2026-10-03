package screen

import (
	"image"
	"math"
)

// logicalRectToPhysical scales a logical compositor rect into physical pixels.
//
// A scale of 1 leaves the rect unchanged. Values <= 0 are treated as 1.
func logicalRectToPhysical(rect image.Rectangle, scale int) image.Rectangle {
	if scale <= 1 {
		return rect
	}
	return image.Rect(
		scaleCoord(rect.Min.X, scale),
		scaleCoord(rect.Min.Y, scale),
		scaleCoord(rect.Max.X, scale),
		scaleCoord(rect.Max.Y, scale),
	)
}

// scaleCoord multiplies one coordinate by scale, saturating rather than
// wrapping.
//
// A rect wide enough to mean "the whole screen" overflows on multiplication and
// wraps negative, which then misses the output entirely: a rect of MaxInt scaled
// by two becomes negative two, and the frame it is clipped against no longer
// overlaps it. Every caller clips the result against the frame bounds, so
// holding the saturated bound still means the whole frame, which is what the
// caller asked for.
func scaleCoord(value, scale int) int {
	limit := math.MaxInt / scale
	if value > limit {
		return math.MaxInt
	}
	if value < -limit {
		return -math.MaxInt
	}
	return value * scale
}
