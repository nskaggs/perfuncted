package perfuncted

import (
	"context"
	"fmt"
	"image"
)

// Capture keeps captured pixels together with the screen rectangle they
// describe. ScreenRect is in the same desktop coordinate space used by input
// operations, even when Image has been cropped or rebased to a different
// origin.
type Capture struct {
	Image      image.Image
	ScreenRect image.Rectangle
}

// ScreenPoint maps a point in Image coordinates back into desktop coordinates.
// It accounts for both a rebased image origin and image scaling.
func (c Capture) ScreenPoint(pixel image.Point) (image.Point, error) {
	if c.Image == nil {
		return image.Point{}, fmt.Errorf("perfuncted: capture has no image")
	}
	bounds := c.Image.Bounds()
	if bounds.Empty() || c.ScreenRect.Empty() {
		return image.Point{}, fmt.Errorf("perfuncted: capture has empty bounds")
	}
	if !pixel.In(bounds) {
		return image.Point{}, fmt.Errorf("perfuncted: image point %v is outside capture bounds %v", pixel, bounds)
	}

	return image.Pt(
		c.ScreenRect.Min.X+(pixel.X-bounds.Min.X)*c.ScreenRect.Dx()/bounds.Dx(),
		c.ScreenRect.Min.Y+(pixel.Y-bounds.Min.Y)*c.ScreenRect.Dy()/bounds.Dy(),
	), nil
}

// Capture returns pixels together with the desktop rectangle they describe.
// For a non-empty rect, ScreenRect is exactly rect even when a backend rebases
// the returned image to a zero origin. For a full-screen capture, the image
// bounds define the captured desktop rectangle.
func (s *ScreenBundle) Capture(ctx context.Context, rect image.Rectangle) (Capture, error) {
	img, err := s.grab(ctx, rect)
	if err != nil {
		return Capture{}, err
	}
	if img == nil {
		return Capture{}, s.operationError("capture", fmt.Errorf("screen: capture returned nil image"))
	}

	screenRect := rect
	if screenRect.Empty() {
		screenRect = img.Bounds()
	}
	return Capture{Image: img, ScreenRect: screenRect}, nil
}
