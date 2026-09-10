package perfuncted

import (
	"context"
	"fmt"
	"image"
	"math/bits"

	"github.com/nskaggs/perfuncted/internal/util"
)

// Capture keeps captured pixels together with the desktop rectangle they
// represent. Image coordinates may have a different origin or scale than the
// corresponding ScreenRect.
type Capture struct {
	Image      image.Image
	ScreenRect image.Rectangle
}

// ScreenPoint maps a point in Image coordinates to the corresponding desktop
// coordinate. The point identifies an image pixel, so the result is always
// inside ScreenRect rather than at its exclusive maximum edge.
func (c Capture) ScreenPoint(pixel image.Point) (image.Point, error) {
	if util.IsNil(c.Image) {
		return image.Point{}, fmt.Errorf("perfuncted: capture has no image: %w", ErrInvalidArgument)
	}
	imageBounds := c.Image.Bounds()
	imageWidth, imageWidthOK := captureAxisSpan(imageBounds.Min.X, imageBounds.Max.X)
	imageHeight, imageHeightOK := captureAxisSpan(imageBounds.Min.Y, imageBounds.Max.Y)
	screenWidth, screenWidthOK := captureAxisSpan(c.ScreenRect.Min.X, c.ScreenRect.Max.X)
	screenHeight, screenHeightOK := captureAxisSpan(c.ScreenRect.Min.Y, c.ScreenRect.Max.Y)
	if !imageWidthOK || !imageHeightOK || !screenWidthOK || !screenHeightOK {
		return image.Point{}, fmt.Errorf("perfuncted: capture has empty or invalid bounds: %w", ErrInvalidArgument)
	}
	if !pixel.In(imageBounds) {
		return image.Point{}, fmt.Errorf("perfuncted: image point %v is outside capture bounds %v: %w", pixel, imageBounds, ErrInvalidArgument)
	}

	xOffset := scaleCaptureOffset(uint64(pixel.X)-uint64(imageBounds.Min.X), imageWidth, screenWidth)
	yOffset := scaleCaptureOffset(uint64(pixel.Y)-uint64(imageBounds.Min.Y), imageHeight, screenHeight)
	return image.Point{
		X: c.ScreenRect.Min.X + int(xOffset),
		Y: c.ScreenRect.Min.Y + int(yOffset),
	}, nil
}

// Capture captures rect and preserves the requested desktop region represented
// by the returned image. An empty rect is rejected because the screenshot
// backend does not expose a backend-independent desktop rectangle for an
// image-only full-screen capture.
func (s *ScreenBundle) Capture(ctx context.Context, rect image.Rectangle) (Capture, error) {
	s.traceAction("screen", "capture rect=%s", rect)
	if rect.Empty() {
		return Capture{}, fmt.Errorf("screen: capture provenance requires a non-empty rectangle: %w", ErrInvalidArgument)
	}
	img, err := s.grab(ctx, rect)
	if err != nil {
		return Capture{}, err
	}
	if util.IsNil(img) {
		return Capture{}, s.operationError("capture", fmt.Errorf("screen: capture returned nil image"))
	}

	return Capture{Image: img, ScreenRect: rect}, nil
}

func captureAxisSpan(min, max int) (uint64, bool) {
	if max <= min {
		return 0, false
	}
	span := uint64(max) - uint64(min)
	return span, span <= uint64(^uint(0)>>1)
}

func scaleCaptureOffset(offset, sourceSpan, destinationSpan uint64) uint64 {
	high, low := bits.Mul64(offset, destinationSpan)
	quotient, _ := bits.Div64(high, low, sourceSpan)
	return quotient
}
