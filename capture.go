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
	// ExpectedImageBounds records the image bounds the capture was asked to produce,
	// and is the zero Rectangle when the capture did not come from a known request.
	//
	// It exists because a capture smaller than the region it represents is ambiguous on
	// its own. A backend asked for 100x50 and handed back 50x25 has either downscaled
	// the region or returned only part of it, and the two are indistinguishable from the
	// bounds alone: clipping both axes by the same proportion preserves the aspect
	// ratio. Treating that as a downscale maps points onto pixels standing for a
	// different place on screen, and treating it as clipped refuses a legitimate
	// downscale. The request is what tells them apart.
	ExpectedImageBounds image.Rectangle
}

// ReportsDownscale reports whether the capture is a smaller uniform rendering of the
// region it represents. It is only meaningful when the expected bounds are known: a
// capture with no recorded request cannot be told apart from a clipped one, so it is
// never treated as a downscale.
func (c Capture) ReportsDownscale() bool {
	if c.ExpectedImageBounds.Empty() || util.IsNil(c.Image) {
		return false
	}
	actual := c.Image.Bounds()
	// The capture has to be exactly the size that was requested. A capture smaller than
	// the request is the part the backend could get, and one larger is something else
	// again; neither is the downscale the request describes.
	if actual.Dx() != c.ExpectedImageBounds.Dx() || actual.Dy() != c.ExpectedImageBounds.Dy() {
		return false
	}
	return actual.Dx() < c.ScreenRect.Dx() && actual.Dy() < c.ScreenRect.Dy()
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
	// A capture smaller than the region it represents is a downscale only when the
	// recorded request says so. Otherwise it is the part the backend could get, and a
	// point in it stands for somewhere else on screen. translatePointsToImage refuses
	// the same capture, so accepting it here would hand back a desktop coordinate whose
	// inverse this package disowns.
	if (imageWidth < screenWidth || imageHeight < screenHeight) && !c.ReportsDownscale() {
		return image.Point{}, fmt.Errorf(
			"perfuncted: capture %v is smaller than region %v and no request records it as a downscale: %w",
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

	// The grab is requested at the region's own size, so that is the expectation the
	// returned bounds are checked against.
	return Capture{Image: img, ScreenRect: rect, ExpectedImageBounds: rect}, nil
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
