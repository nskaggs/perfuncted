package screen

import (
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/v2/find"
)

// verifiedPhysicalRect converts a logical output-local rectangle into pixels
// only when the complete requested region is inside the captured frame.
func verifiedPhysicalRect(rect image.Rectangle, scale uint32, frameWidth, frameHeight int) (image.Rectangle, error) {
	if rect.Empty() || rect.Min.X < 0 || rect.Min.Y < 0 || scale == 0 || frameWidth <= 0 || frameHeight <= 0 {
		return image.Rectangle{}, fmt.Errorf("%w: invalid output capture geometry rect=%v scale=%d frame=%dx%d", find.ErrUnverifiedCaptureGeometry, rect, scale, frameWidth, frameHeight)
	}
	maxInt := uint64(int(^uint(0) >> 1))
	s := uint64(scale)
	if s > maxInt {
		return image.Rectangle{}, fmt.Errorf("%w: pixel scale overflows int", find.ErrUnverifiedCaptureGeometry)
	}
	coords := [...]int{rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y}
	for _, coord := range coords {
		if uint64(coord) > maxInt/s {
			return image.Rectangle{}, fmt.Errorf("%w: scaled capture rectangle overflows", find.ErrUnverifiedCaptureGeometry)
		}
	}
	physical := image.Rect(rect.Min.X*int(scale), rect.Min.Y*int(scale), rect.Max.X*int(scale), rect.Max.Y*int(scale))
	if !physical.In(image.Rect(0, 0, frameWidth, frameHeight)) {
		return image.Rectangle{}, fmt.Errorf("%w: requested pixels %v are not fully contained in captured frame %dx%d", find.ErrUnverifiedCaptureGeometry, physical, frameWidth, frameHeight)
	}
	return physical, nil
}
