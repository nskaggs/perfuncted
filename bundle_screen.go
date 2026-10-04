package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/screen"
)

// ScreenBundle exposes screen operations without exposing an absent backend to
// callers. A Session always initializes this facade.
type ScreenBundle struct {
	backend screen.Screenshotter
	bundleBase
}

// traceAction shadows the promoted bundleBase method so a nil bundle is handled
// before it is dereferenced.
//
// bundleBase is embedded by value, so the promoted method's receiver is
// &s.bundleBase: calling it on a nil *ScreenBundle dereferences s to compute the
// receiver and panics, before bundleBase's own nil check can run. Every traced
// entry point therefore has to tolerate a nil receiver the way checkAvailable
// does.
func (s *ScreenBundle) traceAction(format string, args ...any) {
	if s == nil {
		return
	}
	s.bundleBase.traceAction("screen", format, args...)
}

func (s *ScreenBundle) checkAvailable(operation string) error {
	if s == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return s.checkBackend(operation, s.backend)
}

func (s *ScreenBundle) close() error {
	if s == nil {
		return nil
	}
	return closeBackend(s.backend)
}

func (s *ScreenBundle) grabHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	if err := s.checkAvailable("hash"); err != nil {
		return 0, err
	}
	s.traceAction("grab-hash rect=%s", rect)
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	hash, err := find.GrabHash(ctx, s.backend, rect, nil)
	return hash, s.operationError("hash", err)
}

func (s *ScreenBundle) grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	s.traceAction("grab rect=%s", rect)
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	img, err := s.backend.Grab(ctx, rect)
	return img, s.operationError("capture", err)
}

// Grab captures rect, or the full screen when rect is empty.
func (s *ScreenBundle) Grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	return s.grab(ctx, rect)
}

// GrabFullHash returns a hash of the full screen.
func (s *ScreenBundle) GrabFullHash(ctx context.Context) (uint32, error) {
	return s.grabHash(ctx, image.Rectangle{})
}

// GrabRegionHash returns a hash of rect.
func (s *ScreenBundle) GrabRegionHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	return s.grabHash(ctx, rect)
}

// GetAllPixels captures the full screen.
func (s *ScreenBundle) GetAllPixels(ctx context.Context) (image.Image, error) {
	s.traceAction("get-all-pixels")
	return s.grab(ctx, image.Rectangle{})
}

// GrabRegion captures rect.
func (s *ScreenBundle) GrabRegion(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("grab-region rect=%s", rect)
	return s.grab(ctx, rect)
}

// CaptureRegion captures rect and writes it as a PNG to path.
func (s *ScreenBundle) CaptureRegion(
	ctx context.Context,
	rect image.Rectangle,
	path string,
) error {
	s.traceAction("capture-region rect=%s path=%q", rect, path)
	img, err := s.grab(ctx, rect)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".perfuncted-capture-*")
	if err != nil {
		return s.operationError("capture", fmt.Errorf("screen: create %q: %w", path, err))
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil && closeErr != nil {
		return s.operationError(
			"capture",
			errors.Join(encodeErr, fmt.Errorf("screen: close %q: %w", path, closeErr)),
		)
	}
	if encodeErr != nil {
		return s.operationError("capture", encodeErr)
	}
	if closeErr != nil {
		return s.operationError("capture", fmt.Errorf("screen: close %q: %w", path, closeErr))
	}
	if err := os.Rename(tempPath, path); err != nil {
		return s.operationError("capture", fmt.Errorf("screen: publish %q: %w", path, err))
	}
	return nil
}

// GetPixel returns the pixel at x and y.
func (s *ScreenBundle) GetPixel(
	ctx context.Context,
	x int,
	y int,
) (color.RGBA, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return color.RGBA{}, err
	}
	s.traceAction("get-pixel x=%d y=%d", x, y)
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	if x == math.MaxInt || y == math.MaxInt {
		return color.RGBA{}, s.operationError("pixel", fmt.Errorf("screen: pixel coordinate overflows one-pixel capture: (%d,%d)", x, y))
	}
	pixel, err := find.FirstPixel(
		ctx,
		s.backend,
		image.Rect(x, y, x+1, y+1),
	)
	if err != nil {
		return color.RGBA{}, s.operationError("pixel", err)
	}
	return pixel, nil
}

// GetMultiplePixels returns pixels at points in the same order.
func (s *ScreenBundle) GetMultiplePixels(
	ctx context.Context,
	points []image.Point,
) ([]color.RGBA, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return nil, err
	}
	s.traceAction("get-multiple-pixels count=%d", len(points))
	out := make([]color.RGBA, len(points))
	if len(points) == 0 {
		return out, nil
	}

	bounds, err := pointsBounds(points)
	if err != nil {
		return nil, s.operationError("pixel", err)
	}
	img, err := s.grab(ctx, bounds)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, s.operationError("pixel", errors.New("screen: capture returned nil image"))
	}
	imgBounds := img.Bounds()
	imagePoints, err := translatePointsToImage(points, bounds, imgBounds)
	if err != nil {
		return nil, s.operationError("pixel", err)
	}
	for i, imagePoint := range imagePoints {
		if !imagePoint.In(imgBounds) {
			return nil, s.operationError(
				"pixel",
				fmt.Errorf("screen: capture bounds %v do not contain requested point %v", imgBounds, points[i]),
			)
		}
		rgba, ok := color.RGBAModel.Convert(
			img.At(imagePoint.X, imagePoint.Y),
		).(color.RGBA)
		if !ok {
			return nil, s.operationError("pixel", errors.New("screen: RGBA conversion returned unexpected type"))
		}
		out[i] = rgba
	}
	return out, nil
}

// WaitForFn waits for fn to accept a captured image.
func (s *ScreenBundle) WaitForFn(
	ctx context.Context,
	rect image.Rectangle,
	fn func(context.Context, image.Image) bool,
	poll time.Duration,
) (image.Image, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return nil, err
	}
	s.traceAction("wait-for-fn rect=%s poll=%s", rect, poll)
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	img, err := find.WaitForFn(ctx, s.backend, rect, fn, poll)
	return img, s.operationError("wait", err)
}

// WaitForSettle runs action and waits for the region to stabilize.
func (s *ScreenBundle) WaitForSettle(
	ctx context.Context,
	rect image.Rectangle,
	action func() error,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	s.traceAction(
		"wait-for-settle rect=%s stable=%d poll=%s",
		rect,
		stable,
		poll,
	)
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	before, err := s.grabHash(ctx, rect)
	if err != nil {
		return 0, err
	}
	if action != nil {
		if actionErr := action(); actionErr != nil {
			return 0, actionErr
		}
	}
	changed, err := find.WaitForChange(
		ctx,
		s.backend,
		rect,
		before,
		poll,
		nil,
	)
	if err != nil {
		return 0, s.operationError("wait-change", err)
	}
	stableHash, err := find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		changed,
		stable,
		poll,
		nil,
	)
	return stableHash, s.operationError("wait-stable", err)
}

// pointsBounds returns the smallest region containing every point, which is the
// region that has to be captured to read them all from one image.
func pointsBounds(points []image.Point) (image.Rectangle, error) {
	for _, point := range points {
		if point.X == math.MaxInt || point.Y == math.MaxInt {
			return image.Rectangle{}, fmt.Errorf(
				"screen: pixel coordinate overflows capture bounds: %v", point,
			)
		}
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, point := range points[1:] {
		minX = min(minX, point.X)
		minY = min(minY, point.Y)
		maxX = max(maxX, point.X)
		maxY = max(maxY, point.Y)
	}
	// The points are pixels to read, so the region has to include the last one
	// even though a rectangle's maximum is exclusive.
	return image.Rect(minX, minY, maxX+1, maxY+1), nil
}

// translatePointsToImage maps points given in desktop coordinates onto the
// pixels of a capture of the region they span.
//
// A capture of a desktop region is returned at the output's pixel scale, so the
// mapping is a ratio along each axis rather than a constant offset: with a
// doubled scale the point half way across the region is half way across the
// image, not at the same pixel offset. This is the inverse of Capture.ScreenPoint
// and uses the same rounding, so the two directions agree.
func translatePointsToImage(
	points []image.Point,
	region image.Rectangle,
	imageBounds image.Rectangle,
) ([]image.Point, error) {
	regionWidth, regionWidthOK := captureAxisSpan(region.Min.X, region.Max.X)
	regionHeight, regionHeightOK := captureAxisSpan(region.Min.Y, region.Max.Y)
	imageWidth, imageWidthOK := captureAxisSpan(imageBounds.Min.X, imageBounds.Max.X)
	imageHeight, imageHeightOK := captureAxisSpan(imageBounds.Min.Y, imageBounds.Max.Y)
	if !regionWidthOK || !regionHeightOK || !imageWidthOK || !imageHeightOK {
		return nil, errors.New("screen: capture region or image has empty bounds")
	}

	// A capture that came back smaller than the requested region is not a scaled
	// version of the whole region: it is the part the backend could actually get.
	// Scaling the region into it anyway maps a point near the clipped edge onto a
	// pixel that stands for a different place on screen. Require the two axes to
	// describe one uniform scale before translating anything, so a clipped capture
	// is refused rather than sampled from the wrong location.
	if imageWidth*regionHeight != imageHeight*regionWidth {
		return nil, fmt.Errorf(
			"screen: capture %v is not a uniform scaling of region %v: the capture is clipped",
			imageBounds, region,
		)
	}
	// A capture carries at least one pixel per pixel of the region it covers, because
	// output scaling multiplies the region's pixels and never divides them. A ratio
	// below one is therefore not a smaller scale: it is a capture that came back with
	// part of the region missing, and reading a pixel out of it yields an averaged
	// value standing for somewhere else on screen.
	//
	// Clipping proportionally hides this. A region 100x50 whose right and bottom edges
	// are both cut by the same proportion comes back 50x25, which keeps the aspect
	// ratio and so passes a uniform-scale check, while every point past the midpoint
	// reads a different place on screen than the one asked for.
	if imageWidth < regionWidth || imageHeight < regionHeight {
		return nil, fmt.Errorf(
			"screen: capture %v has fewer pixels than region %v: the capture is clipped",
			imageBounds, region,
		)
	}

	out := make([]image.Point, 0, len(points))
	for _, point := range points {
		if !point.In(region) {
			return nil, fmt.Errorf(
				"screen: point %v is outside the captured region %v",
				point, region,
			)
		}
		out = append(out, image.Point{
			X: imageBounds.Min.X + int(scaleCaptureOffset(uint64(point.X-region.Min.X), regionWidth, imageWidth)),
			Y: imageBounds.Min.Y + int(scaleCaptureOffset(uint64(point.Y-region.Min.Y), regionHeight, imageHeight)),
		})
	}
	return out, nil
}

// WaitForNoChange waits until rect remains unchanged for stable samples.
func (s *ScreenBundle) WaitForNoChange(
	ctx context.Context,
	rect image.Rectangle,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	hash, err := find.WaitForNoChange(
		ctx,
		s.backend,
		rect,
		stable,
		poll,
		nil,
	)
	return hash, s.operationError("wait-stable", err)
}

// WaitForNoChangeFrom waits for rect to remain unchanged from initial.
func (s *ScreenBundle) WaitForNoChangeFrom(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	hash, err := find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		initial,
		stable,
		poll,
		nil,
	)
	return hash, s.operationError("wait-stable", err)
}

// FindColor returns the first pixel in rect within tolerance of target.
func (s *ScreenBundle) FindColor(
	ctx context.Context,
	rect image.Rectangle,
	target color.RGBA,
	tolerance int,
) (image.Point, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return image.Point{}, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	point, err := find.FindColor(ctx, s.backend, rect, target, tolerance)
	return point, s.operationError("pixel", err)
}

// WaitForChange waits until rect differs from initial.
func (s *ScreenBundle) WaitForChange(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-change"); err != nil {
		return 0, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	hash, err := find.WaitForChange(
		ctx,
		s.backend,
		rect,
		initial,
		poll,
		nil,
	)
	return hash, s.operationError("wait-change", err)
}

// WaitFor waits until rect has the requested hash.
func (s *ScreenBundle) WaitFor(
	ctx context.Context,
	rect image.Rectangle,
	want uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return 0, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	hash, err := find.WaitFor(ctx, s.backend, rect, want, poll, nil)
	return hash, s.operationError("wait", err)
}

// ScanFor waits for any requested hash across the supplied rectangles.
func (s *ScreenBundle) ScanFor(
	ctx context.Context,
	rects []image.Rectangle,
	wants []uint32,
	poll time.Duration,
) (find.Result, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return find.Result{}, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	result, err := find.ScanFor(ctx, s.backend, rects, wants, poll, nil)
	return result, s.operationError("wait", err)
}

// Resolution returns the active capture resolution.
func (s *ScreenBundle) Resolution(ctx context.Context) (int, int, error) {
	if err := s.checkAvailable("resolution"); err != nil {
		return 0, 0, err
	}
	ctx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	width, height, err := screen.ResolutionWithContext(ctx, s.backend)
	return width, height, s.operationError("resolution", err)
}
