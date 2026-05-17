package screen

import "image"

// logicalRectToPhysical scales a logical compositor rect into physical pixels.
//
// A scale of 1 leaves the rect unchanged. Values <= 0 are treated as 1.
func logicalRectToPhysical(rect image.Rectangle, scale int) image.Rectangle {
	if scale <= 1 {
		return rect
	}
	return image.Rect(
		rect.Min.X*scale,
		rect.Min.Y*scale,
		rect.Max.X*scale,
		rect.Max.Y*scale,
	)
}
