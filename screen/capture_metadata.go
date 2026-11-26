package screen

import (
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/capture"
	"image"
	"math/bits"

	"github.com/nskaggs/perfuncted/v2/internal/util"
)

// Capture keeps captured pixels together with the desktop rectangle they
// represent. Image coordinates may have a different origin or scale than the
// corresponding ScreenRect.
type Capture struct {
	Image      image.Image
	ScreenRect image.Rectangle
}

// WindowCaptureMode identifies how a window capture obtains its pixels.
type WindowCaptureMode string

const (
	// WindowCaptureVisibleScreenCrop captures the target's reported screen
	// rectangle from the visible desktop composite.
	WindowCaptureVisibleScreenCrop WindowCaptureMode = "visible-screen-crop"
	// WindowCaptureBackgroundPixmap reads the target's own already-redirected
	// X11 Composite pixmap, independent of overlapping desktop windows.
	WindowCaptureBackgroundPixmap WindowCaptureMode = "background-window-pixmap"
)

// WindowCaptureState is the state established alongside a capture.
type WindowCaptureState string

const (
	// WindowCaptureForeground means the target is focused and no occlusion was established.
	WindowCaptureForeground WindowCaptureState = "foreground"
	// WindowCaptureBackgroundVisible means an inactive target has no mapped sibling overlap.
	WindowCaptureBackgroundVisible WindowCaptureState = "background-visible"
	// WindowCaptureOccluded means a mapped top-level sibling overlaps the target bounds.
	WindowCaptureOccluded WindowCaptureState = "occluded"
	// WindowCaptureStateUnknown means visibility could not be established.
	WindowCaptureStateUnknown WindowCaptureState = "unknown"
)

var (
	// ErrWindowCaptureMinimized identifies a target that is minimized, so its
	// background buffer may be absent or stale.
	ErrWindowCaptureMinimized = errors.New("perfuncted: minimized window has no freshness-guaranteed background buffer")
	// ErrWindowCaptureUnmapped identifies a target that is not currently mapped
	// and viewable by its screen backend.
	ErrWindowCaptureUnmapped = errors.New("perfuncted: target window is unmapped or unviewable")
)

// WindowCapture describes pixels captured for a stable window handle. Mode
// identifies whether pixels came from the desktop crop or the target's own
// background buffer. State is authoritative only for background-pixmap mode;
// visible-screen crops report unknown visibility. GeometrySource records how
// screen bounds were established.
type WindowCapture struct {
	Capture        `json:"-"`
	WindowID       string             `json:"window_id"`
	Title          string             `json:"title"`
	AppID          string             `json:"app_id,omitempty"`
	PID            int32              `json:"pid,omitempty"`
	ScreenRect     image.Rectangle    `json:"screen_rect"`
	PixelBounds    image.Rectangle    `json:"pixel_bounds"`
	PixelScaleX    float64            `json:"pixel_scale_x"`
	PixelScaleY    float64            `json:"pixel_scale_y"`
	Mode           WindowCaptureMode  `json:"mode"`
	State          WindowCaptureState `json:"state,omitempty"`
	GeometrySource string             `json:"geometry_source"`
	Occlusion      string             `json:"occlusion"`
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
		return image.Point{}, fmt.Errorf("perfuncted: capture has no image: %w", capability.ErrInvalidArgument)
	}
	imageBounds := c.Image.Bounds()
	imageWidth, imageWidthOK := capture.AxisSpan(imageBounds.Min.X, imageBounds.Max.X)
	imageHeight, imageHeightOK := capture.AxisSpan(imageBounds.Min.Y, imageBounds.Max.Y)
	screenWidth, screenWidthOK := capture.AxisSpan(c.ScreenRect.Min.X, c.ScreenRect.Max.X)
	screenHeight, screenHeightOK := capture.AxisSpan(c.ScreenRect.Min.Y, c.ScreenRect.Max.Y)
	if !imageWidthOK || !imageHeightOK || !screenWidthOK || !screenHeightOK {
		return image.Point{}, fmt.Errorf("perfuncted: capture has empty or invalid bounds: %w", capability.ErrInvalidArgument)
	}
	if !pixel.In(imageBounds) {
		return image.Point{}, fmt.Errorf("perfuncted: image point %v is outside capture bounds %v: %w", pixel, imageBounds, capability.ErrInvalidArgument)
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
			imageBounds, c.ScreenRect, capability.ErrInvalidArgument,
		)
	}
	if imageWidth < screenWidth || imageHeight < screenHeight {
		return image.Point{}, fmt.Errorf(
			"perfuncted: capture %v is smaller than region %v: it cannot be told apart from a clipped capture: %w",
			imageBounds, c.ScreenRect, capability.ErrInvalidArgument,
		)
	}

	xOffset := capture.ScaleOffset(uint64(pixel.X)-uint64(imageBounds.Min.X), imageWidth, screenWidth)
	yOffset := capture.ScaleOffset(uint64(pixel.Y)-uint64(imageBounds.Min.Y), imageHeight, screenHeight)
	return image.Point{
		X: c.ScreenRect.Min.X + int(xOffset),
		Y: c.ScreenRect.Min.Y + int(yOffset),
	}, nil
}
