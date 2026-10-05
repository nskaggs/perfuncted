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

// ReportsDownscale reports whether the capture is smaller than the region it represents.
//
// It is always false, and that is the point. A capture smaller than its region is either
// a downscale or a region clipped on both axes by the same proportion, and the two
// preserve the same aspect ratio, so the bounds cannot separate them. Telling them apart
// needs the scale the backend applied, and the screenshot interface reports only an
// image, so nothing here can know which happened. Rather than record a provenance no
// caller can supply, a capture smaller than its region is refused in both directions.
// When the backend interface can report the scale it applied, this can distinguish a
// genuine downscale from a clipped capture instead of refusing both.
func (c Capture) ReportsDownscale() bool {
	return false
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
	// A capture that is not a uniform scaling of the region is refused rather than mapped
	// through. translatePointsToImage refuses the same captures, so accepting one here
	// would hand back a desktop coordinate whose inverse this package disowns.
	//
	// Both axes have to describe one scale. Comparing them separately is not enough: a
	// capture twice as wide as the region and half as tall passes both size checks and
	// maps a point onto a pixel standing for a different place on screen. The products
	// are compared through 128 bits because the spans come from int coordinates, so on a
	// wide enough rectangle the 64-bit products wrap and two spans of different aspect
	// ratios compare equal.
	lhsHi, lhsLo := bits.Mul64(imageWidth, screenHeight)
	rhsHi, rhsLo := bits.Mul64(imageHeight, screenWidth)
	if lhsHi != rhsHi || lhsLo != rhsLo {
		return image.Point{}, fmt.Errorf(
			"perfuncted: capture %v is not a uniform scaling of region %v: %w",
			imageBounds, c.ScreenRect, ErrInvalidArgument,
		)
	}
	if imageWidth < screenWidth || imageHeight < screenHeight {
		return image.Point{}, fmt.Errorf(
			"perfuncted: capture %v is smaller than region %v: it cannot be told apart from a clipped capture: %w",
			imageBounds, c.ScreenRect, ErrInvalidArgument,
		)
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
	s.traceAction("capture rect=%s", rect)
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
